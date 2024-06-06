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

	if targetAddr != "" {
		serverConn, err := net.Dial("tcp", targetAddr)
		if err != nil {
			log.Printf("Failed to connect to %s: %v", targetAddr, err)
			return
		}
		defer serverConn.Close()

		log.Printf("Established connection from %s to %s", client.RemoteAddr(), targetAddr)

		go func() {
			_, err := io.Copy(serverConn, client)
			if err != nil {
				log.Printf("Error while copying data from client to server: %v", err)
			}
		}()
		_, err = io.Copy(client, serverConn)
		if err != nil {
			log.Printf("Error while copying data from server to client: %v", err)
		}
	} else {
		log.Println("No target address provided, closing connection.")
	}
}

// startClient starts a client that forwards connections to a target address
func startClient(localAddr, targetAddr string) {
	listener, err := net.Listen("tcp", localAddr)
	if err != nil {
		log.Fatalf("Failed to listen on %s: %v", localAddr, err)
	}
	defer listener.Close()
	log.Printf("Client listening on %s and forwarding to %s", localAddr, targetAddr)

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Failed to accept connection: %v", err)
			continue
		}
		go handleClient(conn, targetAddr)
	}
}

// startServer starts a server that only accepts connections
func startServer(listenAddr string) {
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		log.Fatalf("Failed to listen on %s: %v", listenAddr, err)
	}
	defer listener.Close()
	log.Printf("Server listening on %s", listenAddr)

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Failed to accept connection: %v", err)
			continue
		}
		log.Printf("Accepted connection from %s", conn.RemoteAddr())
		// Simply close the connection after accepting
		conn.Close()
	}
}

func main() {
	var mode, localPort, destination, serverAddress string
	flag.StringVar(&mode, "mode", ClientMode, "Mode of operation (client or server)")
	flag.StringVar(&localPort, "localPort", "", "Local port to listen on for client mode")
	flag.StringVar(&destination, "destination", "", "Destination address for client mode (host:port)")
	flag.StringVar(&serverAddress, "server-address", "", "Server address to listen on for server mode")
	flag.Parse()

	switch mode {
	case ClientMode:
		if localPort == "" || destination == "" {
			log.Fatal("Please provide localPort and destination for client mode")
		}
		localAddr := fmt.Sprintf(":%s", localPort)
		startClient(localAddr, destination)
	case ServerMode:
		if serverAddress == "" {
			log.Fatal("Please provide server-address for server mode")
		}
		startServer(serverAddress)
	default:
		log.Fatalf("Invalid mode: %s. Please specify 'client' or 'server'", mode)
	}
}
