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

// handleClient manages connections based on the provided target address
func handleClient(client net.Conn, targetAddr string) {
	defer client.Close()

	serverConn, err := net.Dial("tcp", targetAddr)
	if err != nil {
		log.Printf("Failed to connect to %s: %v", targetAddr, err)
		return
	}
	defer serverConn.Close()

	log.Printf("Established connection from %s to %s", client.RemoteAddr(), targetAddr)

	// Transfer data between client and server
	go func() {
		_, err := io.CopyBuffer(serverConn, client, make([]byte, BufferSize))
		if err != nil {
			log.Printf("Error while copying data from client to server: %v", err)
		}
		serverConn.Close()
		client.Close()
	}()
	_, err = io.CopyBuffer(client, serverConn, make([]byte, BufferSize))
	if err != nil {
		log.Printf("Error while copying data from server to client: %v", err)
	}
	client.Close()
	serverConn.Close()
}

// startServer starts a server on the specified address
func startServer(listenAddr, targetAddr string) {
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		log.Fatalf("Failed to listen on %s: %v", listenAddr, err)
	}
	defer listener.Close()
	log.Printf("Server listening on %s and forwarding to %s", listenAddr, targetAddr)

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Failed to accept connection: %v", err)
			continue
		}
		go handleClient(conn, targetAddr)
	}
}

func main() {
	var mode, localPort, destination, serverAddress string
	flag.StringVar(&mode, "mode", ClientMode, "Mode of operation (client or server)")
	flag.StringVar(&localPort, "localPort", "", "Local port to listen on")
	flag.StringVar(&destination, "destination", "", "Destination address for client mode (host:port)")
	flag.StringVar(&serverAddress, "server-address", "", "Server address to listen on for server mode")
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
		startServer(serverAddress, destination)
	default:
		log.Fatalf("Invalid mode: %s. Please specify 'client' or 'server'", mode)
	}
}
