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
	BufferSize = 128 * 1024 // 32KB buffer size
)

func handleClient(client net.Conn, serverAddr string) {
	defer client.Close()

	// Establish a connection to the server if a server address is provided
	if serverAddr != "" {
		serverConn, err := net.Dial("tcp", serverAddr)
		if err != nil {
			log.Printf("Failed to connect to server %s: %v\n", serverAddr, err)
			return
		}
		defer serverConn.Close()

		// Transfer data concurrently in both directions
		go io.CopyBuffer(serverConn, client, make([]byte, BufferSize))
		io.CopyBuffer(client, serverConn, make([]byte, BufferSize))
	} else {
		log.Println("No destination address provided for server mode, handling local connections only.")
	}
}

func startServer(listenAddr, targetAddr string) {
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		log.Fatalf("Failed to listen on %s: %v", listenAddr, err)
	}
	defer listener.Close()
	log.Printf("Server listening on %s", listenAddr)
	if targetAddr != "" {
		log.Printf(" and forwarding to %s", targetAddr)
	}
	log.Println()

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Failed to accept connection: %v\n", err)
			continue
		}
		log.Printf("Accepted connection from %s", conn.RemoteAddr())
		go handleClient(conn, targetAddr)
	}
}

func main() {
	var mode, localPort, destination, serverAddress string
	flag.StringVar(&mode, "mode", ClientMode, "Mode of operation (client or server)")
	flag.StringVar(&localPort, "localPort", "", "Local port to listen on")
	flag.StringVar(&destination, "destination", "", "Destination address for client mode (host:port)")
	flag.StringVar(&serverAddress, "server-address", "", "Server address to listen on for server gettingMode")
	flag.Parse()

	switch mode {
	case ClientMode:
		if localPort == "" || destination == "" {
			log.Fatal("Please provide localPort and destination for client mode")
		}
		localAddr := fmt.Sprintf(":%s", localPort)
		startServer(localAddr, destination)
	case ServerMode:
		if serverAddress == "" {
			log.Fatal("Please provide server-address for server mode")
		}
		startServer(serverAddress, "")
	default:
		log.Fatalf("Invalid mode: %s. Please specify 'client' or 'server'", mode)
	}
}
