package main

import (
	"context"
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


	"github.com/valyala/fasthttp"
)

var bufferPool = sync.Pool{
	New: func() interface{} {
		return make([]byte, 64*1024) // Increased buffer size for better throughput
	},
}

// Forwarder sets up a listening port and forwards data to the same port on the destination host.
type Forwarder struct {
	ListenPort int
}

// Start begins listening on the forwarder's configured port and forwards connections.
func (f *Forwarder) Start(ctx context.Context, destinationHost string, bufferSize int, workerPool chan struct{}, wg *sync.WaitGroup) {
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
		select {
		case <-ctx.Done():
			return
		default:
			conn, err := listener.Accept()
			if err != nil {
				log.Printf("Failed to accept connection: %v", err)
				continue
			}

			select {
			case workerPool <- struct{}{}:
				go f.handleConnection(ctx, conn, targetAddr, bufferSize, workerPool)
			default:
				log.Printf("Worker pool full, dropping connection from %s", conn.RemoteAddr().String())
				conn.Close()
			}
		}
	}
}

// handleConnection forwards a single connection to the destination host and port.
func (f *Forwarder) handleConnection(ctx context.Context, src net.Conn, targetAddr string, bufferSize int, workerPool chan struct{}) {
	defer src.Close()
	defer func() { <-workerPool }()

	dst, err := net.Dial("tcp", targetAddr)
	if err != nil {
		log.Printf("Failed to connect to target %s: %v", targetAddr, err)
		return
	}
	defer dst.Close()

	err = f.copyData(ctx, src, dst, bufferSize)
	if err != nil {
		log.Printf("Error copying data from %s to %s: %v", src.RemoteAddr().String(), targetAddr, err)
		return
	}
}

// copyData handles the actual data transfer between the source and destination.
func (f *Forwarder) copyData(ctx context.Context, src, dst net.Conn, bufferSize int) error {
	wg := &sync.WaitGroup{}
	wg.Add(2)

	errChan := make(chan error, 2)

	go func() {
		defer wg.Done()
		buf := bufferPool.Get().([]byte)
		defer bufferPool.Put(buf)
		_, err := io.CopyBuffer(dst, src, buf[:bufferSize])
		errChan <- err
	}()

	go func() {
		defer wg.Done()
		buf := bufferPool.Get().([]byte)
		defer bufferPool.Put(buf)
		_, err := io.CopyBuffer(src, dst, buf[:bufferSize])
		errChan <- err
	}()

	wg.Wait()
	close(errChan)

	for err := range errChan {
		if err != nil && err != io.EOF {
			return err
		}
	}
	return nil
}

func main() {
	var wg sync.WaitGroup

	listenPorts := flag.String("listen-ports", "21212,21213", "Comma-separated list of ports to listen on")
	destinationHost := flag.String("destination-host", "localhost", "Destination host to forward to")
	bufferSize := flag.Int("buffer-size", 64*1024, "Buffer size for TCP connections")
	workerCount := flag.Int("workers", 100, "Number of concurrent workers")
	flag.Parse()

	logFile, err := os.OpenFile("forwarder.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		log.Fatalf("Failed to open log file: %v", err)
	}
	defer logFile.Close()
	log.SetOutput(logFile)

	workerPool := make(chan struct{}, *workerCount)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ports := strings.Split(*listenPorts, ",")
	for _, portStr := range ports {
		port, err := strconv.Atoi(portStr)
		if err != nil {
			log.Fatalf("Invalid port number: %v", err)
		}

		wg.Add(1)
		go (&Forwarder{ListenPort: port}).Start(ctx, *destinationHost, *bufferSize, workerPool, &wg)
	}

	runtime.GOMAXPROCS(runtime.NumCPU())

	// Profiling server
	go func() {
		log.Println(fasthttp.ListenAndServe(":6060", pprofHandler))
	}()

	wg.Wait()
}

func pprofHandler(ctx *fasthttp.RequestCtx) {
	switch string(ctx.Path()) {
	case "/debug/pprof/":
		ctx.Redirect("/debug/pprof/", fasthttp.StatusMovedPermanently)
	default:
		ctx.Error("Unsupported path", fasthttp.StatusNotFound)
	}
}
