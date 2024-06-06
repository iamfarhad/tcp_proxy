package main

import (
    "flag"
    "io"
    "log"
    "net"
    "sync"
)

func main() {
    mode := flag.String("mode", "client", "Mode: client or server")
    serverAddress := flag.String("server-address", "localhost:2020", "Address for the server to listen on or client to connect to")
    clientPort := flag.String("client-port", "", "Port for the client to listen on (if running in client mode)")
    flag.Parse()

    switch *mode {
    case "server":
        startServer(*serverAddress)
    case "client":
        if *clientPort == "" {
            log.Fatal("Client mode requires a port to listen on.")
        }
        startClient(*serverAddress, *clientPort)
    default:
        log.Fatal("Unknown mode:", *mode)
    }
}

func startServer(address string) {
    log.Printf("Starting server on %s", address)

    ln, err := net.Listen("tcp", address)
    if err != nil {
        log.Fatalf("Error starting server: %v", err)
    }
    defer ln.Close()

    for {
        conn, err := ln.Accept()
        if err != nil {
            log.Printf("Error accepting connection: %v", err)
            continue
        }
        log.Printf("Accepted connection from %s", conn.RemoteAddr())
        go handleServerConnection(conn)
    }
}

func handleServerConnection(conn net.Conn) {
    defer conn.Close()

    serverConn, err := net.Dial("tcp", "localhost:80") // Adjust the destination as needed
    if err != nil {
        log.Printf("Error connecting to backend server: %v", err)
        return
    }
    defer serverConn.Close()

    var wg sync.WaitGroup
    wg.Add(2)

    go relay(conn, serverConn, &wg)
    go relay(serverConn, conn, &wg)

    wg.Wait()
}

func startClient(serverAddress, clientPort string) {
    listenAddress := ":" + clientPort
    log.Printf("Starting client on %s, forwarding to %s", listenAddress, serverAddress)

    ln, err := net.Listen("tcp", listenAddress)
    if err != nil {
        log.Fatalf("Error starting client listener: %v", err)
    }
    defer ln.Close()

    for {
        conn, err := ln.Accept()
        if err != nil {
            log.Printf("Error accepting connection: %v", err)
            continue
        }
        log.Printf("Accepted connection from %s", conn.RemoteAddr())
        go handleClientConnection(conn, serverAddress)
    }
}

func handleClientConnection(clientConn net.Conn, serverAddress string) {
    defer clientConn.Close()

    serverConn, err := net.Dial("tcp", serverAddress)
    if err != nil {
        log.Printf("Error connecting to server %s: %v", serverAddress, err)
        return
    }
    defer serverConn.Close()

    var wg sync.WaitGroup
    wg.Add(2)

    go relay(clientConn, serverConn, &wg)
    go relay(serverConn, clientConn, &wg)

    wg.Wait()
}

func relay(src, dst net.Conn, wg *sync.WaitGroup) {
    defer wg.Done()
    _, err := io.Copy(dst, src)
    if err != nil {
        log.Printf("Error relaying data: %v", err)
    }
    src.Close()
    dst.Close()
}
