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
)

var bufferPool = sync.Pool{
	New: func() interface{} {
		return make([]byte, 2048*1024) // Optimal buffer size for high throughput
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

	dst, err := net.Dial("tcp", targetAddr)
	if err != nil {
		log.Printf("Failed to connect to target %s: %v", targetAddr, err)
		return
	}
	defer dst.Close()

	// Set TCP options
	setTCPOptions(src)
	setTCPOptions(dst)

	// Use two goroutines to handle data transfer in both directions concurrently
	go f.copyData(src, dst, bufferSize)
	go f.copyData(dst, src, bufferSize)
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
		if err := tcpConn.SetReadBuffer(2048 * 1024); err != nil {
			log.Printf("Failed to set SO_RCVBUF: %v", err)
		}
		if err := tcpConn.SetWriteBuffer(2048 * 1024); err != nil {
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
	// Forward to the destination
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

	// Read response
	buf := bufferPool.Get().([]byte)
	defer bufferPool.Put(buf)

	targetConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, _, err := targetConn.ReadFromUDP(buf)
	if err != nil {
		log.Printf("Failed to read from target UDP %s: %v", targetAddr, err)
		return
	}

	// Send response back to the source
	_, err = srcConn.WriteToUDP(buf[:n], srcAddr)
	if err != nil {
		log.Printf("Failed to write to source UDP %s: %v", srcAddr, err)
	}
}

func (f *Forwarder) copyData(src net.Conn, dst net.Conn, bufferSize int) {
	buf := bufferPool.Get().([]byte)
	defer bufferPool.Put(buf)
	_, err := io.CopyBuffer(dst, src, buf[:bufferSize])
	if err != nil && err != io.EOF {
		log.Printf("Error copying data: %v", err)
	}
}

func main() {
	var wg sync.WaitGroup

	listenPorts := flag.String("listen-ports", "21212,21213", "Comma-separated list of ports to listen on")
	destinationHost := flag.String("destination-host", "localhost", "Destination host to forward to")
	bufferSize := flag.Int("buffer-size", 2048*1024, "Buffer size for TCP connections") // Adjusted buffer size
	workerCount := flag.Int("workers", 500, "Number of concurrent workers") // Increased worker pool size
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
