package main

import (
	"flag"
	"fmt"
	"io"
	"net"
)

// تعریف حالت‌ها
const (
	ClientMode = "client"
	ServerMode = "server"
)

func handleClient(client net.Conn, serverAddr string) {
	defer client.Close()

	// برقراری اتصال با سرور مقصد
	serverConn, err := net.Dial("tcp", serverAddr)
	if err != nil {
		fmt.Println("Unable to connect to server:", err)
		return
	}
	defer serverConn.Close()

	// انتقال داده‌های دریافت شده از کلاینت به سرور مقصد
	go func() {
		_, err := io.Copy(serverConn, client)
		if err != nil {
			fmt.Println("Error copying from client to server:", err)
			return
		}
	}()

	// انتقال داده‌های دریافت شده از سرور مقصد به کلاینت
	_, err = io.Copy(client, serverConn)
	if err != nil {
		fmt.Println("Error copying from server to client:", err)
		return
	}
}

func main() {
	// تعریف و پردازش فلگ‌ها
	mode := flag.String("mode", ClientMode, "Mode of operation (client or server)")
	localPort := flag.String("localPort", "", "Local port for client mode")
	destination := flag.String("destination", "", "Destination address for client mode (host:port)")
	serverAddress := flag.String("server-address", "", "Server address for server mode (host:port)")
	flag.Parse()

	// بررسی و تأیید ورودی ها
	switch *mode {
	case ClientMode:
		if *localPort == "" || *destination == "" {
			fmt.Println("Please provide localPort and destination")
			return
		}
	case ServerMode:
		if *serverAddress == "" {
			fmt.Println("Please provide server-address")
			return
		}
	default:
		fmt.Println("Invalid mode. Please provide either 'client' or 'server'")
		return
	}

	// اجرای عملیات متناسب با حالت
	switch *mode {
	case ClientMode:
		// تعریف آدرس‌ها
		localAddr := fmt.Sprintf(":%s", *localPort)
		destinationAddr := *destination

		// شروع گوش دادن به اتصالات در پورت محلی
		listener, err := net.Listen("tcp", localAddr)
		if err != nil {
			fmt.Println("Error listening:", err)
			return
		}
		defer listener.Close()
		fmt.Println("Listening on", localAddr)

		// پذیرش اتصالات و برقراری ارتباط با سرور مقصد
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
		// تعریف آدرس سرور
		serverAddr := *serverAddress

		// شروع گوش دادن به اتصالات در آدرس سرور
		listener, err := net.Listen("tcp", serverAddr)
		if err != nil {
			fmt.Println("Error listening:", err)
			return
		}
		defer listener.Close()
		fmt.Println("Listening on", serverAddr)

		// پذیرش اتصالات و برقراری ارتباط با کلاینت‌ها
		for {
			client, err := listener.Accept()
			if err != nil {
				fmt.Println("Error accepting connection:", err)
				return
			}
			fmt.Println("Accepted connection from", client.RemoteAddr())
			// اینجا می‌توانید هر عملیات دیگری که مربوط به پردازش اتصالات باشد را انجام دهید
			client.Close()
		}
	}
}
