package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
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
		go relayConnection(conn, serverAddress)
	}
}

func handleConnection(conn net.Conn) {
	defer conn.Close()
	io.Copy(conn, conn)
}

func relayConnection(conn net.Conn, serverAddress string) {
	defer conn.Close()

	serverConn, err := net.Dial("tcp", serverAddress)
	if err != nil {
		log.Printf("Error connecting to server: %v", err)
		return
	}
	defer serverConn.Close()

	go io.Copy(serverConn, conn)
	io.Copy(conn, serverConn)
}
