package main

import (
    "bufio"
    "errors"
    "flag"
    "io"
    "log"
    "net"
    "strings"
    "sync"
)

type PortForwarder struct {
    LocalPort   string
    RelayHost   string
    RelayPort   string
    Destination string
}

func parseDestination(destination string) (string, string, error) {
    parts := strings.Split(destination, ":")
    if len(parts) != 4 {
        return "", "", errors.New("invalid destination format, expected format: <relayHost>:<relayPort>:<destHost>:<destPort>")
    }
    relayHostPort := net.JoinHostPort(parts[0], parts[1])
    destHostPort := net.JoinHostPort(parts[2], parts[3])
    return relayHostPort, destHostPort, nil
}

func handleConnection(localConn net.Conn, relayHostPort, destHostPort string) {
    defer localConn.Close()

    relayConn, err := net.Dial("tcp", relayHostPort)
    if err != nil {
        log.Printf("Failed to connect to relay host: %v", err)
        return
    }
    defer relayConn.Close()

    // Send destination address first
    _, err = relayConn.Write([]byte(destHostPort + "\n"))
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

    log.Printf("Listening on port %s and forwarding to %s via relay %s", forwarder.LocalPort, forwarder.Destination, net.JoinHostPort(forwarder.RelayHost, forwarder.RelayPort))

    for {
        conn, err := listener.Accept()
        if err != nil {
            log.Printf("Failed to accept connection: %v", err)
            continue
        }
        go handleConnection(conn, net.JoinHostPort(forwarder.RelayHost, forwarder.RelayPort), forwarder.Destination)
    }
}

type RelayServer struct {
    RelayPort string
}

func handleRelayConnection(localConn net.Conn) {
    defer localConn.Close()

    reader := bufio.NewReader(localConn)
    destAddr, err := reader.ReadString('\n')
    if err != nil {
        log.Printf("Failed to read destination address: %v", err)
        return
    }
    destAddr = strings.TrimSpace(destAddr)
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
    destination := flag.String("destination", "", "Combined relay host and destination in the format <relayHost>:<relayPort>:<destHost>:<destPort> (for client mode)")
    listenPort := flag.String("listenPort", "", "Relay listen port (for relay mode)")

    flag.Parse()

    if *mode == "client" {
        if *localPort == "" || *destination == "" {
            log.Fatal("In client mode, localPort and destination must be specified")
        }
        relayHostPort, destHostPort, err := parseDestination(*destination)
        if err != nil {
            log.Fatal(err)
        }
        parts := strings.Split(relayHostPort, ":")
        forwarder := PortForwarder{
            LocalPort:  *localPort,
            RelayHost:  parts[0],
            RelayPort:  parts[1],
            Destination: destHostPort,
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
