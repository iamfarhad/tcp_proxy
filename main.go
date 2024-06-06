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

		go io.CopyBuffer(serverConn, client, make([]byte, BufferSize))
		io.CopyBuffer(client, serverConn, make([]byte, BufferSize))
	} else {
		// در اینجا می‌توانید منطق خود را برای پردازش اتصالات ورودی اضافه کنید
		log.Println("Handling connection without forwarding.")
		buf := make([]byte, BufferSize)
		for {
			n, err := client.Read(buf)
			if err != nil {
				if err != io.EOF {
					log.Printf("Error reading from client: %v", err)
				}
				break
			}
			log.Printf("Received %d bytes: %s", n, string(buf[:n]))
		}
	}
}

// startServer starts a server on the specified address
func startServer(listenAddr, targetAddr string) {
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
		startServer(serverAddress, "")
	default:
		log.Fatalf("Invalid mode: %s. Please specify 'client' or 'server'", mode)
	}
}
