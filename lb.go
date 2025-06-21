package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// BackendServer represents a backend server
type BackendServer struct {
	Host        string
	Port        string
	URL         string
	IsHealthy   int32 // Using atomic for lock-free operations
	ActiveConns int32 // Track active connections for better load balancing
	TotalReqs   int64 // Total requests served
	FailedReqs  int64 // Failed requests
}

// SetHealthy updates the health status atomically
func (bs *BackendServer) SetHealthy(healthy bool) {
	if healthy {
		atomic.StoreInt32(&bs.IsHealthy, 1)
	} else {
		atomic.StoreInt32(&bs.IsHealthy, 0)
	}
}

// GetHealthy returns the current health status atomically
func (bs *BackendServer) GetHealthy() bool {
	return atomic.LoadInt32(&bs.IsHealthy) == 1
}

// IncrementActiveConns atomically increments active connections
func (bs *BackendServer) IncrementActiveConns() {
	atomic.AddInt32(&bs.ActiveConns, 1)
}

// DecrementActiveConns atomically decrements active connections
func (bs *BackendServer) DecrementActiveConns() {
	atomic.AddInt32(&bs.ActiveConns, -1)
}

// GetActiveConns returns current active connections
func (bs *BackendServer) GetActiveConns() int32 {
	return atomic.LoadInt32(&bs.ActiveConns)
}

// IncrementTotalReqs atomically increments total requests
func (bs *BackendServer) IncrementTotalReqs() {
	atomic.AddInt64(&bs.TotalReqs, 1)
}

// IncrementFailedReqs atomically increments failed requests
func (bs *BackendServer) IncrementFailedReqs() {
	atomic.AddInt64(&bs.FailedReqs, 1)
}

// Config holds the load balancer configuration
type Config struct {
	ListenPort        string
	Backends          []BackendServer
	HealthCheckPeriod time.Duration
	HealthCheckURL    string
	MaxConnections    int
	ConnectionTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	KeepAliveEnabled  bool
	BufferSize        int
}

// LoadBalancer represents our optimized load balancer
type LoadBalancer struct {
	config         Config
	currentIdx     uint64    // Using uint64 for atomic operations
	activeConns    int32     // Track total active connections
	connectionPool sync.Pool // Connection pooling
	bufferPool     sync.Pool // Buffer pooling for memory efficiency
	ctx            context.Context
	cancel         context.CancelFunc
}

// NewLoadBalancer creates a new optimized load balancer instance
func NewLoadBalancer(config Config) *LoadBalancer {
	ctx, cancel := context.WithCancel(context.Background())

	lb := &LoadBalancer{
		config: config,
		ctx:    ctx,
		cancel: cancel,
	}

	// Initialize buffer pool for memory efficiency
	lb.bufferPool = sync.Pool{
		New: func() interface{} {
			return make([]byte, config.BufferSize)
		},
	}

	return lb
}

// getLeastConnectedBackend returns the backend with least active connections
func (lb *LoadBalancer) getLeastConnectedBackend() *BackendServer {
	var bestBackend *BackendServer
	minConns := int32(^uint32(0) >> 1) // Max int32

	for i := range lb.config.Backends {
		backend := &lb.config.Backends[i]
		if backend.GetHealthy() {
			activeConns := backend.GetActiveConns()
			if activeConns < minConns {
				minConns = activeConns
				bestBackend = backend
			}
		}
	}

	return bestBackend
}

// getNextHealthyBackend returns the next backend using weighted round-robin
func (lb *LoadBalancer) getNextHealthyBackend() *BackendServer {
	// Use least connections for better load distribution
	return lb.getLeastConnectedBackend()
}

// startHealthChecker starts the optimized health checking routine
func (lb *LoadBalancer) startHealthChecker() {
	if lb.config.HealthCheckPeriod <= 0 {
		return
	}

	go func() {
		ticker := time.NewTicker(lb.config.HealthCheckPeriod)
		defer ticker.Stop()

		// Initial health check
		lb.performHealthChecks()

		for {
			select {
			case <-ticker.C:
				lb.performHealthChecks()
			case <-lb.ctx.Done():
				return
			}
		}
	}()
}

// performHealthChecks checks health of all backends concurrently
func (lb *LoadBalancer) performHealthChecks() {
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 10) // Limit concurrent health checks

	for i := range lb.config.Backends {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			semaphore <- struct{}{}        // Acquire
			defer func() { <-semaphore }() // Release
			lb.checkBackendHealth(idx)
		}(i)
	}

	wg.Wait()
}

// checkBackendHealth performs optimized health check
func (lb *LoadBalancer) checkBackendHealth(backendIdx int) {
	backend := &lb.config.Backends[backendIdx]
	healthCheckURL := fmt.Sprintf("http://%s%s", backend.URL, lb.config.HealthCheckURL)

	client := &http.Client{
		Timeout: 3 * time.Second, // Reduced timeout for faster detection
		Transport: &http.Transport{
			MaxIdleConns:       10,
			IdleConnTimeout:    30 * time.Second,
			DisableCompression: true,
		},
	}

	resp, err := client.Get(healthCheckURL)
	if err != nil {
		if backend.GetHealthy() {
			fmt.Printf("Health check FAILED for %s: %v\n", backend.URL, err)
		}
		backend.SetHealthy(false)
		return
	}
	defer resp.Body.Close()

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

// printStats prints load balancer statistics
func (lb *LoadBalancer) printStats() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			fmt.Printf("\n=== Load Balancer Stats ===\n")
			fmt.Printf("Total Active Connections: %d\n", atomic.LoadInt32(&lb.activeConns))

			for i, backend := range lb.config.Backends {
				healthy := "UNHEALTHY"
				if backend.GetHealthy() {
					healthy = "HEALTHY"
				}
				fmt.Printf("Backend[%d] %s: Status=%s, Active=%d, Total=%d, Failed=%d\n",
					i, backend.URL, healthy, backend.GetActiveConns(),
					atomic.LoadInt64(&backend.TotalReqs), atomic.LoadInt64(&backend.FailedReqs))
			}
			fmt.Printf("============================\n\n")
		case <-lb.ctx.Done():
			return
		}
	}
}

// Start begins listening with optimized connection handling
func (lb *LoadBalancer) Start() error {
	// Start health checker
	lb.startHealthChecker()

	// Start stats printer
	go lb.printStats()

	listener, err := net.Listen("tcp", ":"+lb.config.ListenPort)
	if err != nil {
		return fmt.Errorf("failed to start listener: %v", err)
	}
	defer listener.Close()

	fmt.Printf("🚀 Optimized Load Balancer listening on port %s\n", lb.config.ListenPort)
	fmt.Printf("Health check period: %v\n", lb.config.HealthCheckPeriod)
	fmt.Printf("Health check URL: %s\n", lb.config.HealthCheckURL)
	fmt.Printf("Max connections: %d\n", lb.config.MaxConnections)
	fmt.Printf("Connection timeout: %v\n", lb.config.ConnectionTimeout)
	fmt.Printf("Backend servers:\n")
	for i, backend := range lb.config.Backends {
		fmt.Printf("  [%d] %s\n", i, backend.URL)
	}
	fmt.Println()

	// Connection semaphore to limit concurrent connections
	connSemaphore := make(chan struct{}, lb.config.MaxConnections)

	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-lb.ctx.Done():
				return nil
			default:
				log.Printf("Error accepting connection: %v", err)
				continue
			}
		}

		// Non-blocking connection limiting
		select {
		case connSemaphore <- struct{}{}:
			atomic.AddInt32(&lb.activeConns, 1)
			// Handle connection in goroutine
			go func() {
				defer func() {
					<-connSemaphore
					atomic.AddInt32(&lb.activeConns, -1)
				}()
				lb.handleConnection(conn)
			}()
		default:
			// Too many connections, reject immediately
			conn.Close()
			fmt.Printf("Connection rejected: max connections (%d) reached\n", lb.config.MaxConnections)
		}
	}
}

// handleConnection processes an incoming client connection with optimizations
func (lb *LoadBalancer) handleConnection(clientConn net.Conn) {
	defer clientConn.Close()

	// Set timeouts
	clientConn.SetReadDeadline(time.Now().Add(lb.config.ReadTimeout))
	clientConn.SetWriteDeadline(time.Now().Add(lb.config.WriteTimeout))

	// Get backend server
	backend := lb.getNextHealthyBackend()
	if backend == nil {
		errorResponse := "HTTP/1.1 503 Service Unavailable\r\nContent-Length: 21\r\nConnection: close\r\n\r\nNo servers available\n"
		clientConn.Write([]byte(errorResponse))
		return
	}

	// Track connection for this backend
	backend.IncrementActiveConns()
	backend.IncrementTotalReqs()
	defer backend.DecrementActiveConns()

	// Connect to backend with timeout
	backendAddr := backend.Host + ":" + backend.Port
	backendConn, err := net.DialTimeout("tcp", backendAddr, lb.config.ConnectionTimeout)
	if err != nil {
		backend.SetHealthy(false)
		backend.IncrementFailedReqs()
		errorResponse := "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 15\r\nConnection: close\r\n\r\nService Error\n"
		clientConn.Write([]byte(errorResponse))
		return
	}
	defer backendConn.Close()

	// Set backend connection timeouts
	backendConn.SetDeadline(time.Now().Add(30 * time.Second))

	// Use efficient bidirectional copying
	lb.proxyConnections(clientConn, backendConn)
}

// proxyConnections efficiently proxies data between client and backend
func (lb *LoadBalancer) proxyConnections(client, backend net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)

	// Client to Backend
	go func() {
		defer wg.Done()
		lb.copyWithBuffer(backend, client, "client->backend")
	}()

	// Backend to Client
	go func() {
		defer wg.Done()
		lb.copyWithBuffer(client, backend, "backend->client")
	}()

	wg.Wait()
}

// copyWithBuffer efficiently copies data using pooled buffers
func (lb *LoadBalancer) copyWithBuffer(dst, src net.Conn, direction string) {
	buffer := lb.bufferPool.Get().([]byte)
	defer lb.bufferPool.Put(buffer)

	_, err := io.CopyBuffer(dst, src, buffer)
	if err != nil && !isConnectionClosed(err) {
		log.Printf("Error copying data (%s): %v", direction, err)
	}
}

// isConnectionClosed checks if error is due to connection being closed
func isConnectionClosed(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "use of closed network connection") ||
		strings.Contains(err.Error(), "connection reset by peer") ||
		err == io.EOF
}

// parseBackends parses backend server strings with auto-detection
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

		if _, err := strconv.Atoi(port); err != nil {
			log.Printf("Invalid port in backend: %s", backendStr)
			continue
		}

		backend := BackendServer{
			Host:      host,
			Port:      port,
			URL:       fmt.Sprintf("%s:%s", host, port),
			IsHealthy: 1, // Start as healthy
		}
		backends = append(backends, backend)
	}

	return backends
}

func main() {
	// Optimized default configuration
	config := Config{
		ListenPort:        "80",
		HealthCheckPeriod: 5 * time.Second, // Faster health checks
		HealthCheckURL:    "/",
		MaxConnections:    1000,            // Configurable max connections
		ConnectionTimeout: 3 * time.Second, // Faster connection timeout
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		KeepAliveEnabled:  true,
		BufferSize:        32768, // 32KB buffer for efficient copying
		Backends: []BackendServer{
			{Host: "127.0.0.1", Port: "8080", URL: "127.0.0.1:8080", IsHealthy: 1},
			{Host: "127.0.0.1", Port: "8081", URL: "127.0.0.1:8081", IsHealthy: 1},
		},
	}

	// Enhanced command line parsing
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
			i++
			for i < len(args) && !strings.HasPrefix(args[i], "-") {
				backendStrings = append(backendStrings, args[i])
				i++
			}
			i--
		case "--max-connections":
			if i+1 < len(args) {
				maxConns, err := strconv.Atoi(args[i+1])
				if err != nil {
					log.Fatalf("Invalid max connections: %s", args[i+1])
				}
				config.MaxConnections = maxConns
				i++
			}
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
		case "--connection-timeout":
			if i+1 < len(args) {
				seconds, err := strconv.Atoi(args[i+1])
				if err != nil {
					log.Fatalf("Invalid connection timeout: %s", args[i+1])
				}
				config.ConnectionTimeout = time.Duration(seconds) * time.Second
				i++
			}
		case "--buffer-size":
			if i+1 < len(args) {
				size, err := strconv.Atoi(args[i+1])
				if err != nil {
					log.Fatalf("Invalid buffer size: %s", args[i+1])
				}
				config.BufferSize = size
				i++
			}
		case "--no-health-check":
			config.HealthCheckPeriod = 0
		case "-h", "--help":
			fmt.Println("🚀 Optimized Load Balancer")
			fmt.Println("Usage: ./lb [options]")
			fmt.Println("Options:")
			fmt.Println("  -p, --port <port>                     Listen port (default: 80)")
			fmt.Println("  -b, --backends <host:port>            Backend servers")
			fmt.Println("  --max-connections <num>               Maximum concurrent connections (default: 1000)")
			fmt.Println("  --health-check-period <seconds>       Health check interval (default: 5)")
			fmt.Println("  --health-check-url <path>             Health check URL path (default: /)")
			fmt.Println("  --connection-timeout <seconds>        Backend connection timeout (default: 3)")
			fmt.Println("  --buffer-size <bytes>                 Buffer size for copying (default: 32768)")
			fmt.Println("  --no-health-check                     Disable health checking")
			fmt.Println("  -h, --help                            Show this help")
			fmt.Println()
			fmt.Println("Examples:")
			fmt.Println("  ./lb")
			fmt.Println("  ./lb -p 8000 --max-connections 2000")
			fmt.Println("  ./lb -b 127.0.0.1:8080 127.0.0.1:8081 127.0.0.1:8082 127.0.0.1:8083")
			fmt.Println("  ./lb --health-check-period 3 --connection-timeout 2")
			os.Exit(0)
		}
	}

	// Use specified backends if provided
	if len(backendStrings) > 0 {
		config.Backends = parseBackends(backendStrings)
		if len(config.Backends) == 0 {
			log.Fatal("No valid backend servers specified")
		}
	}

	if len(config.Backends) == 0 {
		log.Fatal("At least one backend server must be configured")
	}

	fmt.Printf("🎯 Configured %d backend servers with optimized load balancing\n", len(config.Backends))

	// Create and start optimized load balancer
	lb := NewLoadBalancer(config)
	if err := lb.Start(); err != nil {
		log.Fatalf("Failed to start load balancer: %v", err)
	}
}
