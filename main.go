package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

var bufferPool = sync.Pool{
	New: func() interface{} {
		return make([]byte, 512*1024) // Increased buffer size for better throughput
	},
}

type Forwarder struct {
	ListenPort int
}

func (f *Forwarder) Start(destinationHost string, bufferSize int, workerPool chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()

	listenAddr := fmt.Sprintf(":%d", f.ListenPort)
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		log.Fatalf("Failed to listen on %s: %v", listenAddr, err)
	}
	defer listener.Close()

	targetAddr := fmt.Sprintf("%s:%d", destinationHost, f.ListenPort)
	log.Printf("Listening on %s and forwarding to %s", listenAddr, targetAddr)

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Failed to accept connection: %v", err)
			continue
		}

		select {
		case workerPool <- struct{}{}:
			go f.handleConnection(conn, targetAddr, bufferSize, workerPool)
		default:
			log.Printf("Worker pool full, dropping connection from %s", conn.RemoteAddr().String())
			conn.Close()
		}
	}
}

func (f *Forwarder) handleConnection(src net.Conn, targetAddr string, bufferSize int, workerPool chan struct{}) {
	defer src.Close()
	defer func() { <-workerPool }()

	dst, err := dial(targetAddr)
	if err != nil {
		log.Printf("Failed to connect to target %s: %v", targetAddr, err)
		return
	}
	defer dst.Close()

	// Set TCP options
	setTCPOptions(src)
	setTCPOptions(dst)

	err = f.copyData(src, dst, bufferSize)
	if err != nil {
		log.Printf("Error copying data from %s to %s: %v", src.RemoteAddr().String(), targetAddr, err)
		return
	}
}

func dial(address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("invalid address %s: %v", address, err)
	}
	portInt, err := strconv.Atoi(port)
	if err != nil {
		return nil, fmt.Errorf("invalid port %s: %v", port, err)
	}

	ips, err := net.LookupIP(host)
	if err != nil {
		return nil, fmt.Errorf("failed to lookup IP for host %s: %v", host, err)
	}

	var conn net.Conn
	for _, ip := range ips {
		addr := &net.TCPAddr{
			IP:   ip,
			Port: portInt,
		}
		conn, err = net.DialTCP("tcp", nil, addr)
		if err == nil {
			return conn, nil
		}
	}

	return nil, fmt.Errorf("failed to connect to any resolved address for %s", address)
}

func setTCPOptions(conn net.Conn) {
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		// Disable Nagle's algorithm
		if err := tcpConn.SetNoDelay(true); err != nil {
			log.Printf("Failed to set TCP_NODELAY: %v", err)
		}

		// Set send and receive buffer sizes
		if err := tcpConn.SetReadBuffer(512 * 1024); err != nil {
			log.Printf("Failed to set SO_RCVBUF: %v", err)
		}
		if err := tcpConn.SetWriteBuffer(512 * 1024); err != nil {
			log.Printf("Failed to set SO_SNDBUF: %v", err)
		}

		// Set additional TCP options
		fd, err := tcpConn.File()
		if err != nil {
			log.Printf("Failed to get file descriptor: %v", err)
			return
		}
		defer fd.Close()

		syscall.SetsockoptInt(int(fd.Fd()), syscall.IPPROTO_TCP, syscall.TCP_QUICKACK, 1)
		syscall.SetsockoptInt(int(fd.Fd()), syscall.IPPROTO_TCP, syscall.TCP_WINDOW_CLAMP, 128*1024)
		syscall.SetsockoptInt(int(fd.Fd()), syscall.IPPROTO_TCP, syscall.TCP_MAXSEG, 1460)

	}
}

func (f *Forwarder) copyData(src net.Conn, dst net.Conn, bufferSize int) error {
	errChan := make(chan error, 1)

	go func() {
		buf := bufferPool.Get().([]byte)
		defer bufferPool.Put(buf)
		_, err := io.CopyBuffer(dst, src, buf[:bufferSize])
		errChan <- err
	}()

	go func() {
		buf := bufferPool.Get().([]byte)
		defer bufferPool.Put(buf)
		_, err := io.CopyBuffer(src, dst, buf[:bufferSize])
		errChan <- err
	}()

	err1 := <-errChan
	err2 := <-errChan

	if err1 != nil && err1 != io.EOF {
		return err1
	}
	if err2 != nil && err2 != io.EOF {
		return err2
	}
	return nil
}

func main() {
	var wg sync.WaitGroup

	listenPorts := flag.String("listen-ports", "21212,21213", "Comma-separated list of ports to listen on")
	destinationHost := flag.String("destination-host", "localhost", "Destination host to forward to")
	bufferSize := flag.Int("buffer-size", 512*1024, "Buffer size for TCP connections") // Increased buffer size
	workerCount := flag.Int("workers", 100, "Number of concurrent workers")
	pprofPort := flag.String("pprof-port", "6060", "Port for pprof HTTP server")
	flag.Parse()

	logFile, err := os.OpenFile("forwarder.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		log.Fatalf("Failed to open log file: %v", err)
	}
	defer logFile.Close()
	log.SetOutput(logFile)

	workerPool := make(chan struct{}, *workerCount)

	ports := strings.Split(*listenPorts, ",")
	for _, portStr := range ports {
		port, err := strconv.Atoi(portStr)
		if err != nil {
			log.Fatalf("Invalid port number: %v", err)
		}

		wg.Add(1)
		go (&Forwarder{ListenPort: port}).Start(*destinationHost, *bufferSize, workerPool, &wg)
	}

	runtime.GOMAXPROCS(runtime.NumCPU())

	go func() {
		log.Printf("Starting pprof HTTP server on :%s", *pprofPort)
		if err := http.ListenAndServe(fmt.Sprintf(":%s", *pprofPort), nil); err != nil {
			log.Fatalf("Failed to start pprof HTTP server: %v", err)
		}
	}()

	wg.Wait()
}
