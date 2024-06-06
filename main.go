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
	"time"
	_ "net/http/pprof"
)

var bufferPool = sync.Pool{
	New: func() interface{} {
		return make([]byte, 512*1024) // Optimal buffer size for high throughput
	},
}

type Forwarder struct {
	ListenPort int
}

func (f *Forwarder) StartTCP(destinationHost string, bufferSize int, workerPool chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()

	listenAddr := fmt.Sprintf(":%d", f.ListenPort)
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		log.Fatalf("Failed to listen on %s: %v", listenAddr, err)
	}
	defer listener.Close()

	targetAddr := fmt.Sprintf("%s:%d", destinationHost, f.ListenPort)
	log.Printf("TCP: Listening on %s and forwarding to %s", listenAddr, targetAddr)

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Failed to accept connection: %v", err)
			continue
		}

		select {
		case workerPool <- struct{}{}:
			go f.handleTCPConnection(conn, targetAddr, bufferSize, workerPool)
		default:
			log.Printf("Worker pool full, dropping connection from %s", conn.RemoteAddr().String())
			conn.Close()
		}
	}
}

func (f *Forwarder) handleTCPConnection(src net.Conn, targetAddr string, bufferSize int, workerPool chan struct{}) {
	defer src.Close()
	defer func() { <-workerPool }()

	var dst net.Conn
	var err error
	retries := 3

	for i := 0; i < retries; i++ { // Retry logic for connecting to the target
		dst, err = net.DialTimeout("tcp", targetAddr, 15*time.Second)
		if err == nil {
			break
		}
		log.Printf("Failed to connect to target %s: %v. Retrying (%d/%d)...", targetAddr, err, i+1, retries)
		time.Sleep(time.Duration(2<<i) * time.Second) // Exponential backoff
	}

	if err != nil {
		log.Printf("Failed to connect to target %s after %d attempts: %v", targetAddr, retries, err)
		return
	}
	defer dst.Close()

	// Set TCP options
	setTCPOptions(src)
	setTCPOptions(dst)

	errChan := make(chan error, 2)
	go f.copyData(src, dst, bufferSize, errChan)
	go f.copyData(dst, src, bufferSize, errChan)

	for i := 0; i < 2; i++ {
		if err := <-errChan; err != nil && err != io.EOF {
			log.Printf("Error during data copy: %v", err)
		}
	}
}

func setTCPOptions(conn net.Conn) {
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		// Disable Nagle's algorithm
		if err := tcpConn.SetNoDelay(true); err != nil {
			log.Printf("Failed to set TCP_NODELAY: %v", err)
		}

		// Enable keep-alive
		if err := tcpConn.SetKeepAlive(true); err != nil {
			log.Printf("Failed to set TCP keep-alive: %v", err)
		}
		if err := tcpConn.SetKeepAlivePeriod(1 * time.Minute); err != nil {
			log.Printf("Failed to set TCP keep-alive period: %v", err)
		}

		// Set send and receive buffer sizes
		if err := tcpConn.SetReadBuffer(512 * 1024); err != nil {
			log.Printf("Failed to set SO_RCVBUF: %v", err)
		}
		if err := tcpConn.SetWriteBuffer(512 * 1024); err != nil {
			log.Printf("Failed to set SO_SNDBUF: %v", err)
		}
	}
}

func (f *Forwarder) StartUDP(destinationHost string, bufferSize int, wg *sync.WaitGroup) {
	defer wg.Done()

	listenAddr := fmt.Sprintf(":%d", f.ListenPort)
	udpAddr, err := net.ResolveUDPAddr("udp", listenAddr)
	if err != nil {
		log.Fatalf("Failed to resolve UDP address %s: %v", listenAddr, err)
	}

	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		log.Fatalf("Failed to listen on UDP %s: %v", listenAddr, err)
	}
	defer conn.Close()

	targetAddr := fmt.Sprintf("%s:%d", destinationHost, f.ListenPort)
	targetUDPAddr, err := net.ResolveUDPAddr("udp", targetAddr)
	if err != nil {
		log.Fatalf("Failed to resolve target UDP address %s: %v", targetAddr, err)
	}

	log.Printf("UDP: Listening on %s and forwarding to %s", listenAddr, targetAddr)

	buf := make([]byte, bufferSize)
	for {
		n, srcAddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			log.Printf("Failed to read from UDP: %v", err)
			continue
		}

		go f.handleUDPConnection(conn, srcAddr, targetUDPAddr, buf[:n])
	}
}

func (f *Forwarder) handleUDPConnection(srcConn *net.UDPConn, srcAddr *net.UDPAddr, targetAddr *net.UDPAddr, data []byte) {
	targetConn, err := net.DialUDP("udp", nil, targetAddr)
	if err != nil {
		log.Printf("Failed to connect to target UDP %s: %v", targetAddr, err)
		return
	}
	defer targetConn.Close()

	_, err = targetConn.Write(data)
	if err != nil {
		log.Printf("Failed to write to target UDP %s: %v", targetAddr, err)
		return
	}

	buf := bufferPool.Get().([]byte)
	defer bufferPool.Put(buf)

	targetConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, _, err := targetConn.ReadFromUDP(buf)
	if err != nil {
		log.Printf("Failed to read from target UDP %s: %v", targetAddr, err)
		return
	}

	_, err = srcConn.WriteToUDP(buf[:n], srcAddr)
	if err != nil {
		log.Printf("Failed to write to source UDP %s: %v", srcAddr, err)
	}
}

func (f *Forwarder) copyData(src net.Conn, dst net.Conn, bufferSize int, errChan chan error) {
	buf := bufferPool.Get().([]byte)
	defer bufferPool.Put(buf)
	_, err := io.CopyBuffer(dst, src, buf[:bufferSize])
	if err != nil && err != io.EOF {
		if !strings.Contains(err.Error(), "use of closed network connection") && !strings.Contains(err.Error(), "connection reset by peer") {
			log.Printf("Error copying data: %v", err)
		}
	}
	errChan <- err
}

func main() {
	var wg sync.WaitGroup

	listenPorts := flag.String("listen-ports", "21212,21213", "Comma-separated list of ports to listen on")
	destinationHost := flag.String("destination-host", "localhost", "Destination host to forward to")
	bufferSize := flag.Int("buffer-size", 512*1024, "Buffer size for TCP connections")
	workerCount := flag.Int("workers", 5000, "Number of concurrent workers")
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
		go (&Forwarder{ListenPort: port}).StartTCP(*destinationHost, *bufferSize, workerPool, &wg)
		wg.Add(1)
		go (&Forwarder{ListenPort: port}).StartUDP(*destinationHost, *bufferSize, &wg)
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
