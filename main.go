package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

var bufferPool = sync.Pool{
	New: func() interface{} {
		return make([]byte, 128*1024) // Adjust buffer size as needed
	},
}

// Forwarder sets up a listening port and forwards data to the same port on the destination host.
type Forwarder struct {
	ListenPort int
}

// Start begins listening on the forwarder's configured port and forwards connections.
func (f *Forwarder) Start(destinationHost string, wg *sync.WaitGroup) {
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

		go f.handleConnection(conn, targetAddr)
	}
}

// handleConnection forwards a single connection to the destination host and port.
func (f *Forwarder) handleConnection(src net.Conn, targetAddr string) {
	defer src.Close()

	dst, err := net.Dial("tcp", targetAddr)
	if err != nil {
		log.Printf("Failed to connect to target %s: %v", targetAddr, err)
		return
	}
	defer dst.Close()

	wg := &sync.WaitGroup{}
	wg.Add(2)
	go copyData(src, dst, wg)
	go copyData(dst, src, wg)
	wg.Wait()
}

// copyData handles the actual data transfer between the source and destination.
func copyData(src, dst net.Conn, wg *sync.WaitGroup) {
	defer wg.Done()

	buf := bufferPool.Get().([]byte)
	defer bufferPool.Put(buf)

	_, err := io.CopyBuffer(dst, src, buf)
	if err != nil {
		log.Printf("Data transfer error: %v", err)
	}
}

func main() {
	var wg sync.WaitGroup

	// Command-line flags
	listenPorts := flag.String("listen-ports", "21212,21213", "Comma-separated list of ports to listen on")
	destinationHost := flag.String("destination-host", "localhost", "Destination host to forward to")
	flag.Parse()

	ports := strings.Split(*listenPorts, ",")
	for _, portStr := range ports {
		port, err := strconv.Atoi(portStr)
		if err != nil {
			log.Fatalf("Invalid port number: %v", err)
		}

		wg.Add(1)
		go (&Forwarder{ListenPort: port}).Start(*destinationHost, &wg)
	}

	// Set GOMAXPROCS to the number of cores available
	runtime.GOMAXPROCS(runtime.NumCPU())

	wg.Wait()
}
