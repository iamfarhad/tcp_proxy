package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"flag"
	"fmt"
	"github.com/xtaci/smux"
	"golang.org/x/sys/unix"
	"io"
	"log"
	"math/big"
	"net"
	"os"
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
	portRange  = flag.String("ports", "21212", "Single port or range of ports to listen on")
	bufferSize = flag.Int("buffer", 64*1024, "Buffer size for copy operations (default: 64*1024)")
	useTLS     = flag.Bool("tls", false, "Use TLS")
	fakeTls    = flag.String("fakeTls", "", "Domain to mimic for fake TLS (implies -tls)")
	mux        = flag.Bool("mux", false, "Enable multiplexing with smux")
	relayPort  = flag.Int("relayPort", 9090, "Relay port to forward specific ports to")
	certFile   = flag.String("certFile", "certs/cert.pem", "Path to the TLS certificate file")
	keyFile    = flag.String("keyFile", "certs/key.pem", "Path to the TLS key file")
	mode       = flag.String("mode", "client", "Mode to run: client or relay")
)

type ConnPool struct {
	target    string
	tlsConfig *tls.Config
	pool      chan interface{}
	mux       bool
	mu        sync.Mutex
}

func NewConnPool(target string, tlsConfig *tls.Config, size int, mux bool) *ConnPool {
	return &ConnPool{
		target:    target,
		tlsConfig: tlsConfig,
		pool:      make(chan interface{}, size),
		mux:       mux,
	}
}

func (p *ConnPool) Get() (interface{}, error) {
	if p.mux {
		select {
		case sess := <-p.pool:
			if sess != nil && !sess.(*smux.Session).IsClosed() {
				return sess, nil
			}
		default:
			var conn net.Conn
			var err error
			if p.tlsConfig != nil {
				log.Printf("Dialing TLS connection to %s", p.target)
				conn, err = tls.Dial("tcp", p.target, p.tlsConfig)
			} else {
				log.Printf("Dialing plain connection to %s", p.target)
				conn, err = net.Dial("tcp", p.target)
			}
			if err != nil {
				return nil, err
			}
			return smux.Client(conn, nil)
		}
		return nil, fmt.Errorf("failed to get session")
	} else {
		select {
		case conn := <-p.pool:
			if conn != nil {
				return conn, nil
			}
		default:
			if p.tlsConfig != nil {
				log.Printf("Dialing TLS connection to %s", p.target)
				return tls.Dial("tcp", p.target, p.tlsConfig)
			}
			log.Printf("Dialing plain connection to %s", p.target)
			return net.Dial("tcp", p.target)
		}
		return nil, fmt.Errorf("failed to get connection")
	}
}

func (p *ConnPool) Put(conn interface{}) {
	if p.mux {
		select {
		case p.pool <- conn:
		default:
			conn.(*smux.Session).Close()
		}
	} else {
		select {
		case p.pool <- conn:
		default:
			if p.tlsConfig != nil {
				conn.(*tls.Conn).Close()
			} else {
				conn.(net.Conn).Close()
			}
		}
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

	if pool.mux {
		sess := conn.(*smux.Session)
		stream, err := sess.OpenStream()
		if err != nil {
			log.Printf("Failed to open stream: %v\n", err)
			return
		}
		defer stream.Close()

		tcpConn := src.(*net.TCPConn)
		setTCPKeepAlive(tcpConn)
		setTCPNoDelay(tcpConn)
		optimizeTCPWindowSize(tcpConn)

		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			io.CopyBuffer(stream, src, buffer)
		}()
		go func() {
			defer wg.Done()
			io.CopyBuffer(src, stream, buffer)
		}()

		wg.Wait()
	} else {
		var tcpDstConn *net.TCPConn
		if pool.tlsConfig != nil {
			dst := conn.(*tls.Conn)
			tcpDstConn = dst.NetConn().(*net.TCPConn)
		} else {
			tcpDstConn = conn.(*net.TCPConn)
		}
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
}

func startListener(port int, targetBase, listenBase string, buffer []byte, tlsConfig *tls.Config, mux bool, relayPort int) {
	listenAddr := fmt.Sprintf("%s:%d", listenBase, port)
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		log.Printf("Failed to listen on port %d: %v\n", port, err)
		return
	}
	defer listener.Close()

	log.Printf("Listening on %s\n", listenAddr)

	target := fmt.Sprintf("%s:%d", formatTargetAddress(targetBase), relayPort)
	pool := NewConnPool(target, tlsConfig, 100, mux)

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Failed to accept connection: %v\n", err)
			continue
		}
		go handleRelayConnection(conn, pool, buffer, port)
	}
}

func handleRelayConnection(src net.Conn, pool *ConnPool, buffer []byte, originalPort int) {
	defer src.Close()

	conn, err := pool.Get()
	if err != nil {
		log.Printf("Failed to get connection: %v\n", err)
		return
	}
	defer pool.Put(conn)

	// Send the original port number to the relay server
	// Send the original port number to the relay server
	if _, err := fmt.Fprintf(conn.(io.Writer), "%d\n", originalPort); err != nil {
		log.Printf("Failed to send original port to relay: %v\n", err)
		return
	}

	if pool.mux {
		sess := conn.(*smux.Session)
		stream, err := sess.OpenStream()
		if err != nil {
			log.Printf("Failed to open stream: %v\n", err)
			return
		}
		defer stream.Close()

		tcpConn := src.(*net.TCPConn)
		setTCPKeepAlive(tcpConn)
		setTCPNoDelay(tcpConn)
		optimizeTCPWindowSize(tcpConn)

		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			io.CopyBuffer(stream, src, buffer)
		}()
		go func() {
			defer wg.Done()
			io.CopyBuffer(src, stream, buffer)
		}()

		wg.Wait()
	} else {
		var tcpDstConn *net.TCPConn
		if pool.tlsConfig != nil {
			dst := conn.(*tls.Conn)
			tcpDstConn = dst.NetConn().(*net.TCPConn)
		} else {
			tcpDstConn = conn.(*net.TCPConn)
		}
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
}

func startRelayListener() {
	listener, err := net.Listen("tcp", *listenAddr)
	if err != nil {
		log.Printf("Failed to listen on %s: %v\n", *listenAddr, err)
		return
	}
	defer listener.Close()

	log.Printf("Relay listening on %s\n", *listenAddr)

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Failed to accept connection: %v\n", err)
			continue
		}

		go handleRelay(conn)
	}
}

func handleRelay(src net.Conn) {
	defer src.Close()

	var originalPort int
	_, err := fmt.Fscanf(src, "%d\n", &originalPort)
	if err != nil {
		log.Printf("Failed to read original port: %v\n", err)
		return
	}

	destAddr := fmt.Sprintf("localhost:%d", originalPort)
	dst, err := net.Dial("tcp", destAddr)
	if err != nil {
		log.Printf("Failed to connect to destination %s: %v\n", destAddr, err)
		return
	}
	defer dst.Close()

	log.Printf("Relaying connection from %d to %s\n", originalPort, destAddr)

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		io.Copy(dst, src)
	}()
	go func() {
		defer wg.Done()
		io.Copy(src, dst)
	}()

	wg.Wait()
}

func loadTLSConfig(certFile, keyFile, fakeTls string) (*tls.Config, error) {
	if _, err := os.Stat(certFile); os.IsNotExist(err) {
		err := generateCertificates(certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to generate certificates: %w", err)
		}
	}

	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load key pair: %w", err)
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
	}

	if fakeTls != "" {
		tlsConfig.ServerName = fakeTls
	}

	return tlsConfig, nil
}

func generateCertificates(certFile, keyFile string) error {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("failed to generate private key: %w", err)
	}

	notBefore := time.Now()
	notAfter := notBefore.Add(365 * 24 * time.Hour)

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return fmt.Errorf("failed to generate serial number: %w", err)
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"Self-Signed Org"},
		},
		NotBefore: notBefore,
		NotAfter:  notAfter,

		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return fmt.Errorf("failed to create certificate: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return fmt.Errorf("failed to marshal private key: %w", err)
	}
	keyPEMBlock := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyPEM})

	err = os.MkdirAll("certs", 0755)
	if err != nil {
		return fmt.Errorf("failed to create certs directory: %w", err)
	}

	err = os.WriteFile(certFile, certPEM, 0644)
	if err != nil {
		return fmt.Errorf("failed to write cert file: %w", err)
	}

	err = os.WriteFile(keyFile, keyPEMBlock, 0644)
	if err != nil {
		return fmt.Errorf("failed to write key file: %w", err)
	}

	return nil
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

	log.Printf("Starting with mode: %s", *mode)
	log.Printf("Listen address: %s", *listenAddr)
	log.Printf("Target address: %s", *targetAddr)
	log.Printf("Port range: %s", *portRange)
	log.Printf("Buffer size: %d", *bufferSize)
	log.Printf("Use TLS: %t", *useTLS)
	log.Printf("Fake TLS: %s", *fakeTls)
	log.Printf("Multiplexing (smux) enabled: %t", *mux)
	log.Printf("Relay port: %d", *relayPort)
	log.Printf("Certificate file: %s", *certFile)
	log.Printf("Key file: %s", *keyFile)

	if *mode == "client" {
		runClient()
	} else if *mode == "relay" {
		startRelayListener()
	} else {
		log.Println("Invalid mode. Use -mode=client or -mode=relay.")
	}
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

	var tlsConfig *tls.Config
	var err error
	if *useTLS || *fakeTls != "" {
		tlsConfig, err = loadTLSConfig(*certFile, *keyFile, *fakeTls)
		if err != nil {
			log.Printf("Failed to load TLS config: %v\n", err)
			return
		}
	}

	var wg sync.WaitGroup

	for port := startPort; port <= endPort; port++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			log.Printf("Starting listener on port %d\n", p)
			startListener(p, *targetAddr, *listenAddr, buffer, tlsConfig, *mux, *relayPort)
		}(port)
	}

	wg.Wait()
}


