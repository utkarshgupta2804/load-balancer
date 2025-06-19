package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// BackendServer represents a backend server
type BackendServer struct {
	Host      string
	Port      string
	URL       string
	IsHealthy bool
	mutex     sync.RWMutex // For thread-safe health status updates
}

// SetHealthy updates the health status of the backend server
func (bs *BackendServer) SetHealthy(healthy bool) {
	bs.mutex.Lock()
	defer bs.mutex.Unlock()
	bs.IsHealthy = healthy
}

// GetHealthy returns the current health status of the backend server
func (bs *BackendServer) GetHealthy() bool {
	bs.mutex.RLock()
	defer bs.mutex.RUnlock()
	return bs.IsHealthy
}

// Config holds the load balancer configuration
type Config struct {
	ListenPort        string
	Backends          []BackendServer
	HealthCheckPeriod time.Duration
	HealthCheckURL    string
}

// LoadBalancer represents our load balancer with round-robin scheduling
type LoadBalancer struct {
	config     Config
	currentIdx int
	mutex      sync.Mutex // For thread-safe round-robin index
}

// NewLoadBalancer creates a new load balancer instance
func NewLoadBalancer(config Config) *LoadBalancer {
	return &LoadBalancer{
		config:     config,
		currentIdx: 0,
	}
}

// getNextHealthyBackend returns the next healthy backend server using round-robin algorithm
func (lb *LoadBalancer) getNextHealthyBackend() *BackendServer {
	lb.mutex.Lock()
	defer lb.mutex.Unlock()

	// Find healthy backends
	var healthyBackends []int
	for i, backend := range lb.config.Backends {
		if backend.GetHealthy() {
			healthyBackends = append(healthyBackends, i)
		}
	}

	if len(healthyBackends) == 0 {
		return nil // No healthy backends available
	}

	// Use round-robin among healthy backends
	backendIdx := healthyBackends[lb.currentIdx%len(healthyBackends)]
	lb.currentIdx = (lb.currentIdx + 1) % len(healthyBackends)

	return &lb.config.Backends[backendIdx]
}

// startHealthChecker starts the health checking routine
func (lb *LoadBalancer) startHealthChecker() {
	if lb.config.HealthCheckPeriod <= 0 {
		return // Health checking disabled
	}

	go func() {
		ticker := time.NewTicker(lb.config.HealthCheckPeriod)
		defer ticker.Stop()

		// Initial health check
		lb.performHealthChecks()

		for range ticker.C {
			lb.performHealthChecks()
		}
	}()
}

// performHealthChecks checks the health of all backend servers
func (lb *LoadBalancer) performHealthChecks() {
	var wg sync.WaitGroup

	for i := range lb.config.Backends {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			lb.checkBackendHealth(idx)
		}(i)
	}

	wg.Wait()
}

// checkBackendHealth performs health check on a single backend server
func (lb *LoadBalancer) checkBackendHealth(backendIdx int) {
	backend := &lb.config.Backends[backendIdx]

	// Construct health check URL
	healthCheckURL := fmt.Sprintf("http://%s%s", backend.URL, lb.config.HealthCheckURL)

	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	// Perform health check
	resp, err := client.Get(healthCheckURL)
	if err != nil {
		if backend.GetHealthy() {
			fmt.Printf("Health check FAILED for %s: %v\n", backend.URL, err)
		}
		backend.SetHealthy(false)
		return
	}
	defer resp.Body.Close()

	// Check if status code is 200
	if resp.StatusCode == 200 {
		if !backend.GetHealthy() {
			fmt.Printf("Health check PASSED for %s: Server is back online\n", backend.URL)
		}
		backend.SetHealthy(true)
	} else {
		if backend.GetHealthy() {
			fmt.Printf("Health check FAILED for %s: Status code %d\n", backend.URL, resp.StatusCode)
		}
		backend.SetHealthy(false)
	}
}

// Start begins listening for incoming connections
func (lb *LoadBalancer) Start() error {
	// Start health checker
	lb.startHealthChecker()

	listener, err := net.Listen("tcp", ":"+lb.config.ListenPort)
	if err != nil {
		return fmt.Errorf("failed to start listener: %v", err)
	}
	defer listener.Close()

	fmt.Printf("Load balancer listening on port %s\n", lb.config.ListenPort)
	fmt.Printf("Health check period: %v\n", lb.config.HealthCheckPeriod)
	fmt.Printf("Health check URL: %s\n", lb.config.HealthCheckURL)
	fmt.Printf("Backend servers:\n")
	for i, backend := range lb.config.Backends {
		fmt.Printf("  [%d] %s\n", i, backend.URL)
	}
	fmt.Println()

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Error accepting connection: %v", err)
			continue
		}

		// Handle each connection concurrently
		go lb.handleConnection(conn)
	}
}

// handleConnection processes an incoming client connection
func (lb *LoadBalancer) handleConnection(clientConn net.Conn) {
	defer clientConn.Close()

	// Read the request from client
	request, err := lb.readHTTPRequest(clientConn)
	if err != nil {
		log.Printf("Error reading request: %v", err)
		return
	}

	// Get next healthy backend server using round-robin
	backend := lb.getNextHealthyBackend()
	if backend == nil {
		log.Printf("No healthy backend servers available")
		// Send service unavailable response to client
		errorResponse := "HTTP/1.1 503 Service Unavailable\r\nContent-Length: 21\r\n\r\nNo servers available\n"
		clientConn.Write([]byte(errorResponse))
		return
	}

	// Log the incoming request
	clientAddr := clientConn.RemoteAddr().String()
	fmt.Printf("Received request from %s\n", clientAddr)
	fmt.Printf("Forwarding to backend: %s\n", backend.URL)
	fmt.Print(request)

	// Forward request to selected backend server
	response, err := lb.forwardToBackend(request, *backend)
	if err != nil {
		log.Printf("Error forwarding to backend %s: %v", backend.URL, err)
		// Mark backend as unhealthy if connection fails
		backend.SetHealthy(false)
		// Send error response to client
		errorResponse := "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 15\r\n\r\nService Error\n"
		clientConn.Write([]byte(errorResponse))
		return
	}

	// Log the response from backend
	lines := strings.Split(response, "\r\n")
	if len(lines) > 0 {
		fmt.Printf("Response from %s: %s\n", backend.URL, lines[0])
	}
	fmt.Println()

	// Send response back to client
	_, err = clientConn.Write([]byte(response))
	if err != nil {
		log.Printf("Error writing response to client: %v", err)
	}
}

// readHTTPRequest reads and returns the full HTTP request as a string
func (lb *LoadBalancer) readHTTPRequest(conn net.Conn) (string, error) {
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

// forwardToBackend sends the request to the specified backend server and returns the response
func (lb *LoadBalancer) forwardToBackend(request string, backend BackendServer) (string, error) {
	// Connect to backend server
	backendAddr := backend.Host + ":" + backend.Port
	backendConn, err := net.DialTimeout("tcp", backendAddr, 5*time.Second)
	if err != nil {
		return "", fmt.Errorf("failed to connect to backend: %v", err)
	}
	defer backendConn.Close()

	// Send request to backend
	_, err = backendConn.Write([]byte(request))
	if err != nil {
		return "", fmt.Errorf("failed to send request to backend: %v", err)
	}

	// Read response from backend
	response, err := lb.readHTTPResponse(backendConn)
	if err != nil {
		return "", fmt.Errorf("failed to read response from backend: %v", err)
	}

	return response, nil
}

// readHTTPResponse reads the complete HTTP response from backend
func (lb *LoadBalancer) readHTTPResponse(conn net.Conn) (string, error) {
	reader := bufio.NewReader(conn)
	var responseBuilder strings.Builder

	// Read status line and headers
	contentLength := 0
	for {
		line, _, err := reader.ReadLine()
		if err != nil {
			return "", err
		}

		lineStr := string(line) + "\r\n"
		responseBuilder.WriteString(lineStr)

		// Check for Content-Length header
		if strings.HasPrefix(strings.ToLower(string(line)), "content-length:") {
			fmt.Sscanf(string(line), "Content-Length: %d", &contentLength)
		}

		// Empty line indicates end of headers
		if len(line) == 0 {
			break
		}
	}

	// Read body if Content-Length is specified
	if contentLength > 0 {
		body := make([]byte, contentLength)
		_, err := io.ReadFull(reader, body)
		if err != nil {
			return "", err
		}
		responseBuilder.Write(body)
	} else {
		// If no Content-Length, try to read remaining data with timeout
		conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		remainingData, _ := io.ReadAll(reader)
		if len(remainingData) > 0 {
			responseBuilder.Write(remainingData)
		}
	}

	return responseBuilder.String(), nil
}

// parseBackends parses backend server strings in format "host:port"
func parseBackends(backendStrings []string) []BackendServer {
	var backends []BackendServer

	for _, backendStr := range backendStrings {
		parts := strings.Split(backendStr, ":")
		if len(parts) != 2 {
			log.Printf("Invalid backend format: %s (expected host:port)", backendStr)
			continue
		}

		host := parts[0]
		port := parts[1]

		// Validate port
		if _, err := strconv.Atoi(port); err != nil {
			log.Printf("Invalid port in backend: %s", backendStr)
			continue
		}

		backend := BackendServer{
			Host:      host,
			Port:      port,
			URL:       fmt.Sprintf("%s:%s", host, port),
			IsHealthy: true, // Assume healthy initially
		}
		backends = append(backends, backend)
	}

	return backends
}

func main() {
	// Default configuration with two backend servers
	config := Config{
		ListenPort:        "80",
		HealthCheckPeriod: 10 * time.Second, // Default 10 seconds
		HealthCheckURL:    "/",              // Default root path
		Backends: []BackendServer{
			{Host: "127.0.0.1", Port: "8080", URL: "127.0.0.1:8080", IsHealthy: true},
			{Host: "127.0.0.1", Port: "8081", URL: "127.0.0.1:8081", IsHealthy: true},
		},
	}

	// Parse command line arguments
	args := os.Args[1:]
	var backendStrings []string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-p", "--port":
			if i+1 < len(args) {
				config.ListenPort = args[i+1]
				i++
			}
		case "-b", "--backends":
			// Collect all backend servers
			i++
			for i < len(args) && !strings.HasPrefix(args[i], "-") {
				backendStrings = append(backendStrings, args[i])
				i++
			}
			i-- // Adjust for the outer loop increment
		case "--health-check-period":
			if i+1 < len(args) {
				seconds, err := strconv.Atoi(args[i+1])
				if err != nil {
					log.Fatalf("Invalid health check period: %s", args[i+1])
				}
				config.HealthCheckPeriod = time.Duration(seconds) * time.Second
				i++
			}
		case "--health-check-url":
			if i+1 < len(args) {
				config.HealthCheckURL = args[i+1]
				i++
			}
		case "--no-health-check":
			config.HealthCheckPeriod = 0 // Disable health checking
		case "-h", "--help":
			fmt.Println("Usage: ./lb [options]")
			fmt.Println("Options:")
			fmt.Println("  -p, --port <port>                    Listen port (default: 80)")
			fmt.Println("  -b, --backends <host:port>           Backend servers (default: 127.0.0.1:8080 127.0.0.1:8081)")
			fmt.Println("                                       Example: -b 127.0.0.1:8080 127.0.0.1:8081 127.0.0.1:8082")
			fmt.Println("  --health-check-period <seconds>      Health check interval in seconds (default: 10)")
			fmt.Println("  --health-check-url <path>            Health check URL path (default: /)")
			fmt.Println("  --no-health-check                    Disable health checking")
			fmt.Println("  -h, --help                           Show this help message")
			fmt.Println()
			fmt.Println("Examples:")
			fmt.Println("  ./lb                                                    # Use default backends")
			fmt.Println("  ./lb -p 8000                                           # Listen on port 8000")
			fmt.Println("  ./lb -b 127.0.0.1:8080 127.0.0.1:8081 127.0.0.1:8082 # Specify backends")
			fmt.Println("  ./lb --health-check-period 5                          # Health check every 5 seconds")
			fmt.Println("  ./lb --health-check-url /health                       # Use /health endpoint")
			fmt.Println("  ./lb --no-health-check                                # Disable health checking")
			os.Exit(0)
		}
	}

	// If backends were specified via command line, use them
	if len(backendStrings) > 0 {
		config.Backends = parseBackends(backendStrings)
		if len(config.Backends) == 0 {
			log.Fatal("No valid backend servers specified")
		}
	}

	// Validate that we have at least one backend
	if len(config.Backends) == 0 {
		log.Fatal("At least one backend server must be configured")
	}

	// Create and start load balancer
	lb := NewLoadBalancer(config)
	if err := lb.Start(); err != nil {
		log.Fatalf("Failed to start load balancer: %v", err)
	}
}
