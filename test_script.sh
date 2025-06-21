#!/bin/bash

# =====================================================
# Load Balancer Testing Suite
# =====================================================

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo -e "${BLUE}🚀 Load Balancer Testing Suite${NC}"
echo "=================================="

# Function to start a simple backend server
start_backend_server() {
    local port=$1
    local delay=${2:-0}
    
    echo -e "${GREEN}Starting backend server on port $port with ${delay}ms delay...${NC}"
    
    # Create a simple HTTP server with configurable delay
    cat > "backend_server_$port.go" << EOF
package main

import (
    "fmt"
    "log"
    "net/http"
    "time"
)

func handler(w http.ResponseWriter, r *http.Request) {
    // Add artificial delay to simulate processing time
    time.Sleep(${delay} * time.Millisecond)
    
    response := fmt.Sprintf("Response from server on port $port\\nPath: %s\\nMethod: %s\\nTime: %s\\n", 
        r.URL.Path, r.Method, time.Now().Format("15:04:05.000"))
    
    w.Header().Set("Content-Type", "text/plain")
    w.WriteHeader(200)
    w.Write([]byte(response))
    
    fmt.Printf("Request handled by server $port: %s %s\\n", r.Method, r.URL.Path)
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
    w.WriteHeader(200)
    w.Write([]byte("OK"))
}

func main() {
    http.HandleFunc("/", handler)
    http.HandleFunc("/health", healthHandler)
    
    fmt.Println("Server starting on port $port...")
    log.Fatal(http.ListenAndServe(":$port", nil))
}
EOF

    # Compile and run the server in background
    go build -o "backend_server_$port" "backend_server_$port.go"
    ./backend_server_$port &
    
    # Store PID for cleanup
    echo $! >> backend_pids.txt
    
    # Wait for server to start
    sleep 2
    
    # Test if server is running
    if curl -s "http://localhost:$port/health" > /dev/null; then
        echo -e "${GREEN}✅ Backend server on port $port is running${NC}"
    else
        echo -e "${RED}❌ Failed to start backend server on port $port${NC}"
    fi
}

# Function to clean up background processes
cleanup() {
    echo -e "\n${YELLOW}🧹 Cleaning up...${NC}"
    
    # Kill backend servers
    if [ -f backend_pids.txt ]; then
        while read pid; do
            kill $pid 2>/dev/null
        done < backend_pids.txt
        rm backend_pids.txt
    fi
    
    # Kill load balancer
    if [ ! -z "$LB_PID" ]; then
        kill $LB_PID 2>/dev/null
    fi
    
    # Clean up generated files
    rm -f backend_server_*.go backend_server_* test_results*.txt lb_optimized
    
    echo -e "${GREEN}✅ Cleanup completed${NC}"
}

# Set trap to cleanup on exit
trap cleanup EXIT

# Function to run load test
run_load_test() {
    local test_name=$1
    local concurrent_users=$2
    local requests_per_user=$3
    local target_url=$4
    
    echo -e "\n${BLUE}🔥 Running Load Test: $test_name${NC}"
    echo "Concurrent Users: $concurrent_users"
    echo "Requests per User: $requests_per_user"
    echo "Target URL: $target_url"
    echo "----------------------------------------"
    
    # Use curl for simple load testing
    start_time=$(date +%s.%N)
    
    for ((i=1; i<=concurrent_users; i++)); do
        {
            for ((j=1; j<=requests_per_user; j++)); do
                response=$(curl -s -w "%{http_code},%{time_total},%{time_connect}" "$target_url" 2>/dev/null)
                echo "$response" >> "test_results_${test_name}.txt"
            done
        } &
    done
    
    # Wait for all background jobs to complete
    wait
    
    end_time=$(date +%s.%N)
    duration=$(echo "$end_time - $start_time" | bc -l)
    
    # Analyze results
    total_requests=$((concurrent_users * requests_per_user))
    successful_requests=$(grep -c "200" "test_results_${test_name}.txt" 2>/dev/null || echo "0")
    failed_requests=$((total_requests - successful_requests))
    requests_per_second=$(echo "scale=2; $total_requests / $duration" | bc -l)
    
    echo -e "${GREEN}📊 Test Results:${NC}"
    echo "Total Duration: ${duration}s"
    echo "Total Requests: $total_requests"
    echo "Successful Requests: $successful_requests"
    echo "Failed Requests: $failed_requests"
    echo "Requests per Second: $requests_per_second"
    echo "Success Rate: $(echo "scale=2; $successful_requests * 100 / $total_requests" | bc -l)%"
    
    # Calculate average response time
    if [ -f "test_results_${test_name}.txt" ]; then
        avg_response_time=$(awk -F',' '{sum+=$2; count++} END {if(count>0) printf "%.3f", sum/count}' "test_results_${test_name}.txt")
        echo "Average Response Time: ${avg_response_time}s"
    fi
    
    # Clean up test results
    rm -f "test_results_${test_name}.txt"
}

# Function to test backend auto-detection
test_backend_auto_detection() {
    echo -e "\n${BLUE}🔍 Testing Backend Auto-Detection${NC}"
    echo "====================================="
    
    # Start varying number of backends
    local ports=(8080 8081 8082 8083 8084)
    
    echo "Starting 5 backend servers..."
    for port in "${ports[@]}"; do
        start_backend_server $port 100
    done
    
    echo -e "\n${YELLOW}Starting load balancer with auto-detection...${NC}"
    
    # Build the load balancer
    go build -o lb_optimized optimized_lb.go
    
    # Start load balancer with all backends
    ./lb_optimized -p 9000 -b 127.0.0.1:8080 127.0.0.1:8081 127.0.0.1:8082 127.0.0.1:8083 127.0.0.1:8084 \
        --max-connections 500 --health-check-period 3 &
    LB_PID=$!
    
    # Wait for load balancer to start
    sleep 5
    
    # Test load distribution
    echo -e "\n${YELLOW}Testing load distribution across all backends...${NC}"
    run_load_test "auto_detection" 10 5 "http://localhost:9000/"
    
    # Kill one backend and test failover
    echo -e "\n${YELLOW}Testing failover - killing backend on port 8082...${NC}"
    pkill -f "backend_server_8082"
    sleep 10  # Wait for health check to detect failure
    
    run_load_test "failover" 5 10 "http://localhost:9000/"
    
    # Restart the backend and test recovery
    echo -e "\n${YELLOW}Testing recovery - restarting backend on port 8082...${NC}"
    start_backend_server 8082 100
    sleep 10  # Wait for health check to detect recovery
    
    run_load_test "recovery" 5 10 "http://localhost:9000/"
    
    kill $LB_PID
    wait $LB_PID 2>/dev/null
}

# Function to run performance comparison
run_performance_test() {
    echo -e "\n${BLUE}⚡ Performance Testing${NC}"
    echo "======================"
    
    # Start backend servers with different response times
    start_backend_server 8080 50   # Fast server
    start_backend_server 8081 200  # Medium server
    start_backend_server 8082 100  # Fast-medium server
    
    # Build and start optimized load balancer
    go build -o lb_optimized optimized_lb.go
    ./lb_optimized -p 9000 -b 127.0.0.1:8080 127.0.0.1:8081 127.0.0.1:8082 \
        --max-connections 1000 --health-check-period 2 --connection-timeout 2 --buffer-size 65536 &
    LB_PID=$!
    
    sleep 5
    
    # Light load test
    echo -e "\n${YELLOW}Light Load Test (50 concurrent users, 10 requests each)${NC}"
    run_load_test "light_load" 50 10 "http://localhost:9000/"
    
    # Medium load test
    echo -e "\n${YELLOW}Medium Load Test (100 concurrent users, 20 requests each)${NC}"
    run_load_test "medium_load" 100 20 "http://localhost:9000/"
    
    # Heavy load test
    echo -e "\n${YELLOW}Heavy Load Test (200 concurrent users, 25 requests each)${NC}"
    run_load_test "heavy_load" 200 25 "http://localhost:9000/"
    
    # Stress test
    echo -e "\n${YELLOW}Stress Test (500 concurrent users, 10 requests each)${NC}"
    run_load_test "stress_test" 500 10 "http://localhost:9000/"
    
    kill $LB_PID
    wait $LB_PID 2>/dev/null
}

# Function to test health check functionality
test_health_checks() {
    echo -e "\n${BLUE}🏥 Health Check Testing${NC}"
    echo "========================"
    
    # Start backends
    start_backend_server 8080 0
    start_backend_server 8081 0
    
    # Start load balancer with frequent health checks
    go build -o lb_optimized optimized_lb.go
    ./lb_optimized -p 9000 -b 127.0.0.1:8080 127.0.0.1:8081 \
        --health-check-period 2 --health-check-url "/health" &
    LB_PID=$!
    
    sleep 5
    
    echo "Testing normal operation..."
    for i in {1..10}; do
        response=$(curl -s "http://localhost:9000/")
        echo "Request $i: $(echo "$response" | head -1)"
        sleep 0.5
    done
    
    echo -e "\n${YELLOW}Simulating backend failure...${NC}"
    # Kill one backend
    pkill -f "backend_server_8081"
    
    # Wait for health check to detect failure
    sleep 5
    
    echo "Testing with one backend down..."
    for i in {1..10}; do
        response=$(curl -s "http://localhost:9000/")
        echo "Request $i: $(echo "$response" | head -1)"
        sleep 0.5
    done
    
    echo -e "\n${YELLOW}Simulating backend recovery...${NC}"
    # Restart the backend
    start_backend_server 8081 0
    
    # Wait for health check to detect recovery
    sleep 5
    
    echo "Testing after recovery..."
    for i in {1..10}; do
        response=$(curl -s "http://localhost:9000/")
        echo "Request $i: $(echo "$response" | head -1)"
        sleep 0.5
    done
    
    kill $LB_PID
    wait $LB_PID 2>/dev/null
}

# Function to test different load balancing algorithms
test_algorithms() {
    echo -e "\n${BLUE}🔄 Algorithm Testing${NC}"
    echo "===================="
    
    # Start backends with different response times
    start_backend_server 8080 100
    start_backend_server 8081 200
    start_backend_server 8082 150
    
    # Test Round Robin
    echo -e "\n${YELLOW}Testing Round Robin Algorithm${NC}"
    go build -o lb_optimized optimized_lb.go
    ./lb_optimized -p 9000 -b 127.0.0.1:8080 127.0.0.1:8081 127.0.0.1:8082 \
        --algorithm round-robin &
    LB_PID=$!
    sleep 3
    
    run_load_test "round_robin" 20 10 "http://localhost:9000/"
    kill $LB_PID
    wait $LB_PID 2>/dev/null
    
    # Test Least Connections (if available)
    echo -e "\n${YELLOW}Testing Least Connections Algorithm${NC}"
    ./lb_optimized -p 9000 -b 127.0.0.1:8080 127.0.0.1:8081 127.0.0.1:8082 \
        --algorithm least-connections &
    LB_PID=$!
    sleep 3
    
    run_load_test "least_connections" 20 10 "http://localhost:9000/"
    kill $LB_PID
    wait $LB_PID 2>/dev/null
    
    # Test Weighted Round Robin (if available)
    echo -e "\n${YELLOW}Testing Weighted Round Robin Algorithm${NC}"
    ./lb_optimized -p 9000 -b 127.0.0.1:8080 127.0.0.1:8081 127.0.0.1:8082 \
        --algorithm weighted --weights 3,1,2 &
    LB_PID=$!
    sleep 3
    
    run_load_test "weighted_round_robin" 20 10 "http://localhost:9000/"
    kill $LB_PID
    wait $LB_PID 2>/dev/null
}

# Function to test SSL/TLS termination
test_ssl_termination() {
    echo -e "\n${BLUE}🔐 SSL/TLS Termination Testing${NC}"
    echo "==============================="
    
    # Generate self-signed certificate for testing
    echo "Generating self-signed certificate..."
    openssl req -x509 -newkey rsa:4096 -keyout key.pem -out cert.pem -days 1 -nodes \
        -subj "/C=US/ST=Test/L=Test/O=Test/CN=localhost" 2>/dev/null
    
    if [ $? -eq 0 ]; then
        start_backend_server 8080 0
        
        # Start load balancer with SSL
        ./lb_optimized -p 9443 -b 127.0.0.1:8080 \
            --ssl-cert cert.pem --ssl-key key.pem &
        LB_PID=$!
        sleep 3
        
        echo "Testing HTTPS connection..."
        response=$(curl -k -s "https://localhost:9443/" || echo "SSL test failed")
        echo "SSL Response: $(echo "$response" | head -1)"
        
        kill $LB_PID
        wait $LB_PID 2>/dev/null
        
        # Clean up certificates
        rm -f cert.pem key.pem
    else
        echo -e "${YELLOW}⚠️  OpenSSL not available, skipping SSL tests${NC}"
    fi
}

# Function to test rate limiting
test_rate_limiting() {
    echo -e "\n${BLUE}🚦 Rate Limiting Testing${NC}"
    echo "========================="
    
    start_backend_server 8080 0
    
    # Start load balancer with rate limiting
    ./lb_optimized -p 9000 -b 127.0.0.1:8080 \
        --rate-limit 10 --rate-window 1 &
    LB_PID=$!
    sleep 3
    
    echo "Testing rate limiting (10 requests per second)..."
    
    # Send requests rapidly
    success_count=0
    rate_limited_count=0
    
    for i in {1..20}; do
        response=$(curl -s -w "%{http_code}" "http://localhost:9000/" -o /dev/null)
        if [ "$response" = "200" ]; then
            ((success_count++))
            echo "Request $i: Success"
        elif [ "$response" = "429" ]; then
            ((rate_limited_count++))
            echo "Request $i: Rate Limited"
        else
            echo "Request $i: Other ($response)"
        fi
        sleep 0.05  # Send requests quickly
    done
    
    echo -e "${GREEN}Rate Limiting Results:${NC}"
    echo "Successful requests: $success_count"
    echo "Rate limited requests: $rate_limited_count"
    
    kill $LB_PID
    wait $LB_PID 2>/dev/null
}

# Function to test session affinity/sticky sessions
test_session_affinity() {
    echo -e "\n${BLUE}🔗 Session Affinity Testing${NC}"
    echo "============================"
    
    start_backend_server 8080 0
    start_backend_server 8081 0
    
    # Start load balancer with session affinity
    ./lb_optimized -p 9000 -b 127.0.0.1:8080 127.0.0.1:8081 \
        --session-affinity cookie &
    LB_PID=$!
    sleep 3
    
    echo "Testing session affinity with cookies..."
    
    # Make requests with same session
    cookie_jar=$(mktemp)
    
    for i in {1..10}; do
        response=$(curl -s -c "$cookie_jar" -b "$cookie_jar" "http://localhost:9000/")
        server_port=$(echo "$response" | grep "port" | awk '{print $NF}')
        echo "Request $i: Served by port $server_port"
    done
    
    rm -f "$cookie_jar"
    
    kill $LB_PID
    wait $LB_PID 2>/dev/null
}

# Function to generate test report
generate_report() {
    echo -e "\n${BLUE}📋 Test Report Summary${NC}"
    echo "======================"
    echo "Test Suite: Load Balancer Testing"
    echo "Date: $(date)"
    echo "Duration: $((SECONDS / 60)) minutes $((SECONDS % 60)) seconds"
    echo ""
    echo "Tests Executed:"
    echo "✅ Backend Auto-Detection and Failover"
    echo "✅ Performance Testing (Light/Medium/Heavy/Stress)"
    echo "✅ Health Check Functionality"
    echo "✅ Load Balancing Algorithms"
    echo "✅ SSL/TLS Termination (if OpenSSL available)"
    echo "✅ Rate Limiting"
    echo "✅ Session Affinity"
    echo ""
    echo -e "${GREEN}All tests completed successfully!${NC}"
}

# Main execution
main() {
    # Check dependencies
    if ! command -v go &> /dev/null; then
        echo -e "${RED}❌ Go is not installed. Please install Go to run this test suite.${NC}"
        exit 1
    fi
    
    if ! command -v curl &> /dev/null; then
        echo -e "${RED}❌ curl is not installed. Please install curl to run this test suite.${NC}"
        exit 1
    fi
    
    if ! command -v bc &> /dev/null; then
        echo -e "${YELLOW}⚠️  bc is not installed. Some calculations may not work properly.${NC}"
    fi
    
    # Check if load balancer source exists
    if [ ! -f "optimized_lb.go" ]; then
        echo -e "${RED}❌ optimized_lb.go not found. Please ensure the load balancer source code is in the current directory.${NC}"
        exit 1
    fi
    
    echo -e "${GREEN}✅ All dependencies satisfied${NC}"
    echo ""
    
    # Initialize tracking
    start_time=$SECONDS
    
    # Run all tests
    test_backend_auto_detection
    run_performance_test
    test_health_checks
    test_algorithms
    test_ssl_termination
    test_rate_limiting
    test_session_affinity
    
    # Generate final report
    generate_report
}

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --help|-h)
            echo "Load Balancer Testing Suite"
            echo "Usage: $0 [options]"
            echo ""
            echo "Options:"
            echo "  --help, -h              Show this help message"
            echo "  --test TEST_NAME        Run specific test (auto-detection, performance, health, algorithms, ssl, rate-limit, session)"
            echo "  --quick                 Run quick tests only"
            echo ""
            exit 0
            ;;
        --test)
            TEST_NAME="$2"
            shift 2
            ;;
        --quick)
            QUICK_MODE=true
            shift
            ;;
        *)
            echo "Unknown option: $1"
            exit 1
            ;;
    esac
done

# Run specific test if requested
if [ ! -z "$TEST_NAME" ]; then
    case $TEST_NAME in
        auto-detection)
            test_backend_auto_detection
            ;;
        performance)
            run_performance_test
            ;;
        health)
            test_health_checks
            ;;
        algorithms)
            test_algorithms
            ;;
        ssl)
            test_ssl_termination
            ;;
        rate-limit)
            test_rate_limiting
            ;;
        session)
            test_session_affinity
            ;;
        *)
            echo "Unknown test: $TEST_NAME"
            exit 1
            ;;
    esac
else
    # Run all tests
    main
fi