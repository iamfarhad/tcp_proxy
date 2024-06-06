package main

import (
	"flag"
	"fmt"
	"io"
	"net"
)

const (
	ClientMode = "client"
	ServerMode = "server"
	BufferSize = 1024
)

func handleClient(client net.Conn, serverAddr string) {
	defer client.Close()

	serverConn, err := net.Dial("tcp", serverAddr)
	if err != nil {
		fmt.Println("Unable to connect to server:", err)
		return
	}
	defer serverConn.Close()

	errCh := make(chan error, 1)

	copyData := func(dst io.Writer, src io.Reader) {
		_, err := io.CopyBuffer(dst, src, make([]byte, BufferSize))
		errCh <- err
	}

	go copyData(serverConn, client)
	go copyData(client, serverConn)

	if err := <-errCh; err != nil {
		fmt.Println("Error:", err)
	}

	if err := <-errCh; err != nil {
		fmt.Println("Error:", err)
	}
}

func main() {
	var mode, localPort, destination, serverAddress string
	flag.StringVar(&mode, "mode", ClientMode, "Mode of operation (client or server)")
	flag.StringVar(&localPort, "localPort", "", "Local port for client mode")
	flag.StringVar(&destination, "destination", "", "Destination address for client mode (host:port)")
	flag.StringVar(&serverAddress, "server-address", "", "Server address for server mode (host:port)")
	flag.Parse()

	switch mode {
	case ClientMode:
		if localPort == "" || destination == "" {
			fmt.Println("Please provide localPort and destination")
			return
		}
		localAddr := fmt.Sprintf(":%s", localPort)
		destinationAddr := destination

		listener, err := net.Listen("tcp", localAddr)
		if err != nil {
			fmt.Println("Error listening:", err)
			return
		}
		defer listener.Close()
		fmt.Println("Listening on", localAddr)

		for {
			client, err := listener.Accept()
			if err != nil {
				fmt.Println("Error accepting connection:", err)
				return
			}
			fmt.Println("Accepted connection from", client.RemoteAddr())
			go handleClient(client, destinationAddr)
		}
	case ServerMode:
		if serverAddress == "" {
			fmt.Println("Please provide server-address")
			return
		}
		serverAddr := serverAddress

		listener, err := net.Listen("tcp", serverAddr)
		if err != nil {
			fmt.Println("Error listening:", err)
			return
		}
		defer listener.Close()
		fmt.Println("Listening on", serverAddr)

		for {
			client, err := listener.Accept()
			if err != nil {
				fmt.Println("Error accepting connection:", err)
				return
			}
			fmt.Println("Accepted connection from", client.RemoteAddr())
			client.Close()
		}
	default:
		fmt.Println("Invalid mode. Please provide either 'client' or 'server'")
		return
	}
}
