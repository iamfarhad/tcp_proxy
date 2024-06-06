package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net"
)

const (
	ClientMode = "client"
	ServerMode = "server"
	BufferSize = 32 * 1024 // 32KB buffer size
)

func transferData(src, dst net.Conn) {
	defer dst.Close()
	_, err := io.CopyBuffer(dst, src, make([]byte, BufferSize))
	if err != nil {
		log.Printf("Error transferring data: %v\n", err)
	}
}

func handleConnection(conn net.Conn, target string) {
	defer conn.Close()

	// Connect to the target address
	serverConn, err := net.Dial("tcp", target)
	if err != nil {
		log.Printf("Failed to connect to server %s: %v\n", target, err)
		return
	}
	defer serverConn.Close()

	// Transfer data from the client to the server and vice versa
	go transferData(conn, serverConn)
	transferData(serverConn, conn)
}

func startServer(listenAddr string, targetAddr string) {
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		log.Fatalf("Failed to listen on %s: %v", listenAddr, err)
	}
	defer listener.Close()
	log.Printf("Server listening on %s and forwarding to %s\n", listenAddr, targetAddr)

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Failed to accept connection: %v\n", err)
			continue
		}
		go handleConnection(conn, targetAddr)
	}
}

func main() {
	mode := flag.String("mode", ClientMode, "Mode of operation (client or server)")
	localPort := flag.String("localPort", "8080", "Local port to listen on")
	destination := flag.String("destination", "", "Destination address (host:port) for client mode")
	serverAddress := flag.String("server-address", "", "Server address to listen on for server mode")
	flag.Parse()

	switch *mode {
	case ClientMode:
		if *destination == "" {
			log.Fatal("Destination address must be provided in client mode")
		}
		localAddr := fmt.Sprintf(":%s", *localPort)
		startServer(localAddr, *destination)
	case ServerMode:
		if *serverAddress == "" {
			log.Fatal("Server address must be provided in server mode")
		}
		startServer(*serverAddress, *destination)
	default:
		log.Fatalf("Invalid mode: %s", *mode)
	}
}
