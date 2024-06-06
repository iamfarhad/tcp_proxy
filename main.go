package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

var bufferPool = sync.Pool{
	New: func() interface{} {
		return make([]byte, 32*1024) // Adjust buffer size as needed
	},
}

// Forwarder sets up a listening port and forwards data to the same port on the destination host.
type Forwarder struct {
	ListenPort int
}

// Start begins listening on the forwarder's configured port and forwards connections.
func (f *Forwarder) Start(destinationHost string, bufferSize int, wg *sync.WaitGroup) {
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

		tcpConn, ok := conn.(*net.TCPConn)
		if !ok {
			log.Printf("Failed to cast to TCPConn: %v", err)
			continue
		}

		if err := tcpConn.SetNoDelay(true); err != nil {
			log.Printf("Failed to set TCP_NODELAY: %v", err)
			continue
		}

		if err := tcpConn.SetWriteBuffer(bufferSize); err != nil {
			log.Printf("Failed to set write buffer size: %v", err)
			continue
		}
		if err := tcpConn.SetReadBuffer(bufferSize); err != nil {
			log.Printf("Failed to set read buffer size: %v", err)
			continue
		}

		go f.handleConnection(tcpConn, targetAddr, bufferSize)
	}
}

// handleConnection forwards a single connection to the destination host and port.
func (f *Forwarder) handleConnection(src *net.TCPConn, targetAddr string, bufferSize int) {
	defer src.Close()

	dst, err := net.Dial("tcp", targetAddr)
	if err != nil {
		log.Printf("Failed to connect to target %s: %v", targetAddr, err)
		return
	}
	defer dst.Close()

	dstTcpConn, ok := dst.(*net.TCPConn)
	if !ok {
		log.Printf("Failed to cast to TCPConn: %v", err)
		return
	}

	if err := dstTcpConn.SetNoDelay(true); err != nil {
		log.Printf("Failed to set TCP_NODELAY: %v", err)
		return
	}

	if err := dstTcpConn.SetWriteBuffer(bufferSize); err != nil {
		log.Printf("Failed to set write buffer size: %v", err)
		return
	}
	if err := dstTcpConn.SetReadBuffer(bufferSize); err != nil {
		log.Printf("Failed to set read buffer size: %v", err)
		return
	}

	wg := &sync.WaitGroup{}
	wg.Add(2)
	go copyData(src, dstTcpConn, wg, bufferSize)
	go copyData(dstTcpConn, src, wg, bufferSize)
	wg.Wait()
}

// copyData handles the actual data transfer between the source and destination.
func copyData(src, dst *net.TCPConn, wg *sync.WaitGroup, bufferSize int) {
	defer wg.Done()

	buf := bufferPool.Get().([]byte)
	defer bufferPool.Put(buf)

	_, err := io.CopyBuffer(dst, src, buf[:bufferSize])
	if err != nil {
		log.Printf("Data transfer error: %v", err)
	}
}

func main() {
	var wg sync.WaitGroup

	listenPorts := flag.String("listen-ports", "21212,21213", "Comma-separated list of ports to listen on")
	destinationHost := flag.String("destination-host", "localhost", "Destination host to forward to")
	bufferSize := flag.Int("buffer-size", 32*1024, "Buffer size for TCP connections")
	flag.Parse()

	logFile, err := os.OpenFile("forwarder.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		log.Fatalf("Failed to open log file: %v", err)
	}
	defer logFile.Close()
	log.SetOutput(logFile)

	ports := strings.Split(*listenPorts, ",")
	for _, portStr := range ports {
		port, err := strconv.Atoi(portStr)
		if err != nil {
			log.Fatalf("Invalid port number: %v", err)
		}

		wg.Add(1)
		go (&Forwarder{ListenPort: port}).Start(*destinationHost, *bufferSize, &wg)
	}

	runtime.GOMAXPROCS(runtime.NumCPU())

	wg.Wait()
}
