package main

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
)

// BackendServer represents our backend server
type BackendServer struct {
	port    string
	content string
}

// NewBackendServer creates a new backend server instance
func NewBackendServer(port, content string) *BackendServer {
	return &BackendServer{
		port:    port,
		content: content,
	}
}

// Start begins listening for incoming connections
func (bs *BackendServer) Start() error {
	listener, err := net.Listen("tcp", ":"+bs.port)
	if err != nil {
		return fmt.Errorf("failed to start listener: %v", err)
	}
	defer listener.Close()

	fmt.Printf("Backend server listening on port %s\n", bs.port)

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Error accepting connection: %v", err)
			continue
		}

		// Handle each connection concurrently
		go bs.handleConnection(conn)
	}
}

// handleConnection processes an incoming connection
func (bs *BackendServer) handleConnection(conn net.Conn) {
	defer conn.Close()

	// Read the request
	request, err := bs.readHTTPRequest(conn)
	if err != nil {
		log.Printf("Error reading request: %v", err)
		return
	}

	// Log the incoming request
	clientAddr := conn.RemoteAddr().String()
	fmt.Printf("Received request from %s\n", clientAddr)

	// Extract and print the first line of the request
	lines := strings.Split(request, "\r\n")
	if len(lines) > 0 {
		fmt.Printf("%s\n", lines[0])
	}

	// Create HTML response with server identification
	htmlContent := fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
	<head>
		<meta charset="utf-8">
		<title>Index Page</title>
	</head>
	<body>
		<h1>%s</h1>
		<p>Hello from the web server running on port %s.</p>
		<p>This response demonstrates round-robin load balancing!</p>
	</body>
</html>`, bs.content, bs.port)

	// Send HTTP response
	response := fmt.Sprintf("HTTP/1.1 200 OK\r\n"+
		"Content-Type: text/html\r\n"+
		"Content-Length: %d\r\n"+
		"Connection: close\r\n"+
		"\r\n%s", len(htmlContent), htmlContent)

	_, err = conn.Write([]byte(response))
	if err != nil {
		log.Printf("Error writing response: %v", err)
		return
	}

	fmt.Printf("Replied with HTML content from port %s\n", bs.port)
	fmt.Println()
}

// readHTTPRequest reads and returns the full HTTP request as a string
func (bs *BackendServer) readHTTPRequest(conn net.Conn) (string, error) {
	reader := bufio.NewReader(conn)
	var requestBuilder strings.Builder

	// Read the request line and headers
	for {
		line, _, err := reader.ReadLine()
		if err != nil {
			return "", err
		}

		lineStr := string(line) + "\r\n"
		requestBuilder.WriteString(lineStr)

		// Empty line indicates end of headers
		if len(line) == 0 {
			break
		}
	}

	return requestBuilder.String(), nil
}

func main() {
	// Default port and content
	port := "8080"
	content := "Backend Server"

	// Parse command line arguments
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-p", "--port":
			if i+1 < len(args) {
				port = args[i+1]
				i++
			}
		case "-c", "--content":
			if i+1 < len(args) {
				content = args[i+1]
				i++
			}
		case "-h", "--help":
			fmt.Println("Usage: ./be [options]")
			fmt.Println("Options:")
			fmt.Println("  -p, --port <port>       Listen port (default: 8080)")
			fmt.Println("  -c, --content <text>    Content identifier (default: 'Backend Server')")
			fmt.Println("  -h, --help             Show this help message")
			fmt.Println()
			fmt.Println("Examples:")
			fmt.Println("  ./be -p 8080 -c 'Server A'")
			fmt.Println("  ./be -p 8081 -c 'Server B'")
			os.Exit(0)
		}
	}

	// Set default content based on port if not specified
	if content == "Backend Server" {
		content = fmt.Sprintf("Backend Server %s", port)
	}

	// Create and start backend server
	server := NewBackendServer(port, content)
	if err := server.Start(); err != nil {
		log.Fatalf("Failed to start backend server: %v", err)
	}
}
