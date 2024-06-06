package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"sync"
)

func main() {
	mode := flag.String("mode", "client", "Mode: client or server")
	serverAddress := flag.String("server-address", "localhost:8080", "Address for the server to listen on or client to connect to")
	clientListenPort := flag.String("client-port", "", "Port for the client to listen on (if running in client mode)")
	flag.Parse()

	switch *mode {
	case "server":
		startServer(*serverAddress)
	case "client":
		if *clientListenPort == "" {
			fmt.Println("Client mode requires a port to listen on.")
			flag.Usage()
			os.Exit(1)
		}
		startClient(*serverAddress, *clientListenPort)
	default:
		fmt.Println("Unknown mode:", *mode)
		flag.Usage()
		os.Exit(1)
	}
}

func startServer(address string) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		log.Fatalf("Error starting server: %v", err)
	}
	defer listener.Close()
	log.Printf("Server listening on %s", address)

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Error accepting connection: %v", err)
			continue
		}
		log.Printf("Accepted connection from %s", conn.RemoteAddr())
		go handleConnection(conn)
	}
}

func startClient(serverAddress, clientPort string) {
	listenAddress := ":" + clientPort
	listener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		log.Fatalf("Error starting client listener: %v", err)
	}
	defer listener.Close()
	log.Printf("Client listening on %s", listenAddress)

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Error accepting connection: %v", err)
			continue
		}
		log.Printf("Accepted connection from %s, relaying to server %s", conn.RemoteAddr(), serverAddress)
		go relayConnection(conn, serverAddress)
	}
}

func handleConnection(conn net.Conn) {
	defer conn.Close()
	log.Printf("Handling connection from %s", conn.RemoteAddr())
	buf := make([]byte, 32*1024) // 32KB buffer
	for {
		n, err := conn.Read(buf)
		if err != nil {
			if err != io.EOF {
				log.Printf("Error reading from %s: %v", conn.RemoteAddr(), err)
			}
			break
		}
		if n > 0 {
			log.Printf("Read %d bytes from %s", n, conn.RemoteAddr())
			m, err := conn.Write(buf[:n])
			if err != nil {
				log.Printf("Error writing to %s: %v", conn.RemoteAddr(), err)
				break
			}
			log.Printf("Wrote %d bytes to %s", m, conn.RemoteAddr())
		}
	}
}

func relayConnection(clientConn net.Conn, serverAddress string) {
	defer clientConn.Close()

	serverConn, err := net.Dial("tcp", serverAddress)
	if err != nil {
		log.Printf("Error connecting to server %s: %v", serverAddress, err)
		return
	}
	defer serverConn.Close()

	log.Printf("Relaying data between client %s and server %s", clientConn.RemoteAddr(), serverConn.RemoteAddr())

	var wg sync.WaitGroup
	wg.Add(2)

	go copyData(&wg, serverConn, clientConn, "client to server")
	go copyData(&wg, clientConn, serverConn, "server to client")

	wg.Wait()
}

func copyData(wg *sync.WaitGroup, dst net.Conn, src net.Conn, direction string) {
	defer wg.Done()
	buf := make([]byte, 32*1024) // 32KB buffer
	for {
		n, err := src.Read(buf)
		if err != nil {
			if err != io.EOF {
				log.Printf("Error reading from %s during %s: %v", src.RemoteAddr(), direction, err)
			}
			break
		}
		if n > 0 {
			log.Printf("Read %d bytes from %s during %s: %s", n, src.RemoteAddr(), direction, string(buf[:n]))
			m, err := dst.Write(buf[:n])
			if err != nil {
				log.Printf("Error writing to %s during %s: %v", dst.RemoteAddr(), direction, err)
				break
			}
			log.Printf("Wrote %d bytes to %s during %s", m, dst.RemoteAddr(), direction)
		}
	}
}
