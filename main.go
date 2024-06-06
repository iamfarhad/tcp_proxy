package main

import (
	"flag"
	"fmt"
	"golang.org/x/sys/unix"

	"log"
	"net"

	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	SPLICE_F_MOVE = 1
	SPLICE_F_MORE = 4
)

var (
	listenAddr = flag.String("listen", "0.0.0.0", "TCP listen address (default: 0.0.0.0)")
	targetAddr = flag.String("target", "127.0.0.1", "Target address (IPv4 or IPv6)")
	portRange  = flag.String("ports", "31311-31318", "Single port or range of ports to listen on")
	bufferSize = flag.Int("buffer", 64*1024, "Buffer size for copy operations (default: 64*1024)")
)

type ConnPool struct {
	target string
	pool   chan net.Conn
	mu     sync.Mutex
}

func NewConnPool(target string, size int) *ConnPool {
	return &ConnPool{
		target: target,
		pool:   make(chan net.Conn, size),
	}
}

func (p *ConnPool) Get() (net.Conn, error) {
	select {
	case conn := <-p.pool:
		if conn != nil {
			return conn, nil
		}
	default:
		conn, err := net.Dial("tcp", p.target)
		if err != nil {
			return nil, err
		}
		return conn, nil
	}
	return nil, fmt.Errorf("failed to get connection")
}

func (p *ConnPool) Put(conn net.Conn) {
	select {
	case p.pool <- conn:
	default:
		conn.Close()
	}
}

func setTCPFastOpen(fd int) {
	unix.SetsockoptInt(fd, unix.IPPROTO_TCP, unix.TCP_FASTOPEN, 1)
}

func setTCPNoDelay(conn *net.TCPConn) {
	conn.SetNoDelay(true)
}

func setTCPKeepAlive(conn *net.TCPConn) {
	conn.SetKeepAlive(true)
	conn.SetKeepAlivePeriod(30 * time.Second)
}

func optimizeTCPWindowSize(conn *net.TCPConn) {
	conn.SetReadBuffer(64 * 1024)
	conn.SetWriteBuffer(64 * 1024)
}

func zeroCopy(src, dst *net.TCPConn) error {
	srcFd, err := src.File()
	if err != nil {
		return err
	}
	defer srcFd.Close()

	dstFd, err := dst.File()
	if err != nil {
		return err
	}
	defer dstFd.Close()

	pipeFds := []int{0, 0}
	if err := syscall.Pipe(pipeFds); err != nil {
		return err
	}
	defer syscall.Close(pipeFds[0])
	defer syscall.Close(pipeFds[1])

	for {
		n, err := syscall.Splice(int(srcFd.Fd()), nil, pipeFds[1], nil, 64*1024, SPLICE_F_MOVE|SPLICE_F_MORE)
		if n == 0 || err != nil {
			return err
		}
		_, err = syscall.Splice(pipeFds[0], nil, int(dstFd.Fd()), nil, int(n), SPLICE_F_MOVE|SPLICE_F_MORE)
		if err != nil {
			return err
		}
	}
}

func handleConnection(src net.Conn, pool *ConnPool, buffer []byte) {
	defer src.Close()

	conn, err := pool.Get()
	if err != nil {
		log.Printf("Failed to get connection: %v\n", err)
		return
	}
	defer pool.Put(conn)

	tcpDstConn := conn.(*net.TCPConn)
	tcpSrcConn := src.(*net.TCPConn)

	setTCPKeepAlive(tcpSrcConn)
	setTCPNoDelay(tcpSrcConn)
	optimizeTCPWindowSize(tcpSrcConn)

	setTCPKeepAlive(tcpDstConn)
	setTCPNoDelay(tcpDstConn)
	optimizeTCPWindowSize(tcpDstConn)

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		zeroCopy(tcpSrcConn, tcpDstConn)
	}()
	go func() {
		defer wg.Done()
		zeroCopy(tcpDstConn, tcpSrcConn)
	}()

	wg.Wait()
}

func startListener(port int, targetBase, listenBase string, buffer []byte) {
	listenAddr := fmt.Sprintf("%s:%d", listenBase, port)
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		log.Printf("Failed to listen on port %d: %v\n", port, err)
		return
	}
	defer listener.Close()

	log.Printf("Listening on %s\n", listenAddr)

	target := fmt.Sprintf("%s:%d", formatTargetAddress(targetBase), port)
	pool := NewConnPool(target, 100)

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Failed to accept connection: %v\n", err)
			continue
		}
		go handleConnection(conn, pool, buffer)
	}
}

func formatTargetAddress(addr string) string {
	if strings.Contains(addr, ":") && !strings.Contains(addr, "[") {
		return fmt.Sprintf("[%s]", addr)
	}
	return addr
}

func tuneSystem() {
	rLimit := &unix.Rlimit{
		Cur: 10240,
		Max: 10240,
	}
	if err := unix.Setrlimit(unix.RLIMIT_NOFILE, rLimit); err != nil {
		log.Printf("Failed to set rlimit: %v\n", err)
	}

	sysctl("net.core.rmem_max", "16777216")
	sysctl("net.core.wmem_max", "16777216")
	sysctl("net.ipv4.tcp_rmem", "4096 87380 16777216")
	sysctl("net.ipv4.tcp_wmem", "4096 65536 16777216")
	sysctl("net.ipv4.tcp_congestion_control", "bbr")

	runtime.GOMAXPROCS(runtime.NumCPU())
}

func sysctl(key, value string) {
	cmd := exec.Command("sysctl", "-w", fmt.Sprintf("%s=%s", key, value))
	if err := cmd.Run(); err != nil {
		log.Printf("Failed to set sysctl %s: %v\n", key, err)
	}
}

func main() {
	tuneSystem()
	flag.Parse()

	log.Printf("Starting with listen address: %s", *listenAddr)
	log.Printf("Target address: %s", *targetAddr)
	log.Printf("Port range: %s", *portRange)
	log.Printf("Buffer size: %d", *bufferSize)

	runClient()
}

func runClient() {
	var startPort, endPort int
	ports := strings.Split(*portRange, "-")
	if len(ports) == 1 {
		startPort, _ = strconv.Atoi(ports[0])
		endPort = startPort
	} else if len(ports) == 2 {
		startPort, _ = strconv.Atoi(ports[0])
		endPort, _ = strconv.Atoi(ports[1])
	} else {
		log.Printf("Invalid port range: %v\n", *portRange)
		return
	}

	buffer := make([]byte, *bufferSize)

	var wg sync.WaitGroup

	for port := startPort; port <= endPort; port++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			log.Printf("Starting listener on port %d\n", p)
			startListener(p, *targetAddr, *listenAddr, buffer)
		}(port)
	}

	wg.Wait()
}
