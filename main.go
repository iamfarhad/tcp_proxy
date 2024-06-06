package main

import (
    "flag"
    "io"
    "log"
    "net"
    "sync"
)

type PortForwarder struct {
    LocalPort string
    RelayHost string
    RelayPort string
}

func handleConnection(localConn net.Conn, relayHost, relayPort string) {
    defer localConn.Close()

    relayConn, err := net.Dial("tcp", net.JoinHostPort(relayHost, relayPort))
    if err != nil {
        log.Printf("Failed to connect to relay host: %v", err)
        return
    }
    defer relayConn.Close()

    // Send destination address first
    destAddr := localConn.RemoteAddr().String()
    _, err = relayConn.Write([]byte(destAddr + "\n"))
    if err != nil {
        log.Printf("Failed to send destination address: %v", err)
        return
    }

    var wg sync.WaitGroup
    wg.Add(2)

    go func() {
        defer wg.Done()
        io.Copy(relayConn, localConn)
    }()

    go func() {
        defer wg.Done()
        io.Copy(localConn, relayConn)
    }()

    wg.Wait()
}

func startPortForwarder(forwarder PortForwarder) {
    listener, err := net.Listen("tcp", ":"+forwarder.LocalPort)
    if err != nil {
        log.Fatalf("Failed to start listener on port %s: %v", forwarder.LocalPort, err)
    }
    defer listener.Close()

    log.Printf("Listening on port %s and forwarding to %s:%s", forwarder.LocalPort, forwarder.RelayHost, forwarder.RelayPort)

    for {
        conn, err := listener.Accept()
        if err != nil {
            log.Printf("Failed to accept connection: %v", err)
            continue
        }
        go handleConnection(conn, forwarder.RelayHost, forwarder.RelayPort)
    }
}

type RelayServer struct {
    RelayPort string
}

func handleRelayConnection(localConn net.Conn) {
    defer localConn.Close()

    buf := make([]byte, 4096)
    n, err := localConn.Read(buf)
    if err != nil {
        log.Printf("Failed to read from connection: %v", err)
        return
    }

    destAddr := string(buf[:n])
    destAddr = destAddr[:len(destAddr)-1] // Remove newline character
    log.Printf("Connecting to destination: %s", destAddr)

    remoteConn, err := net.Dial("tcp", destAddr)
    if err != nil {
        log.Printf("Failed to connect to destination: %v", err)
        return
    }
    defer remoteConn.Close()

    var wg sync.WaitGroup
    wg.Add(2)

    go func() {
        defer wg.Done()
        io.Copy(remoteConn, localConn)
    }()

    go func() {
        defer wg.Done()
        io.Copy(localConn, remoteConn)
    }()

    wg.Wait()
}

func startRelayServer(server RelayServer) {
    listener, err := net.Listen("tcp", ":"+server.RelayPort)
    if err != nil {
        log.Fatalf("Failed to start relay server on port %s: %v", server.RelayPort, err)
    }
    defer listener.Close()

    log.Printf("Relay server listening on port %s", server.RelayPort)

    for {
        conn, err := listener.Accept()
        if err != nil {
            log.Printf("Failed to accept connection: %v", err)
            continue
        }
        go handleRelayConnection(conn)
    }
}

func main() {
    mode := flag.String("mode", "", "Mode to run: client or relay")
    localPort := flag.String("localPort", "", "Local port to listen on (for client mode)")
    relayHost := flag.String("relayHost", "", "Relay host to forward to (for client mode)")
    relayPort := flag.String("relayPort", "", "Relay port to forward to (for client mode)")
    listenPort := flag.String("listenPort", "", "Relay listen port (for relay mode)")

    flag.Parse()

    if *mode == "client" {
        if *localPort == "" || *relayHost == "" || *relayPort == "" {
            log.Fatal("In client mode, localPort, relayHost, and relayPort must be specified")
        }
        forwarder := PortForwarder{
            LocalPort:  *localPort,
            RelayHost:  *relayHost,
            RelayPort:  *relayPort,
        }
        startPortForwarder(forwarder)
    } else if *mode == "relay" {
        if *listenPort == "" {
            log.Fatal("In relay mode, listenPort must be specified")
        }
        server := RelayServer{
            RelayPort: *listenPort,
        }
        startRelayServer(server)
    } else {
        log.Fatal("Mode must be either client or relay")
    }
}
