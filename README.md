# Load Balancer with Health Checking

A simple HTTP load balancer implementation in Go that distributes incoming requests across multiple backend servers using round-robin algorithm with health checking capabilities.

## Features

- **Round-Robin Load Balancing**: Distributes requests evenly across healthy backend servers
- **Health Checking**: Periodically monitors backend server health and removes unhealthy servers from rotation
- **Automatic Recovery**: Automatically adds servers back to rotation when they become healthy
- **Concurrent Request Handling**: Uses goroutines to handle multiple client connections simultaneously
- **Configurable**: Command-line options for ports, backends, and health check settings
- **Graceful Failure Handling**: Returns appropriate error codes when no healthy servers are available

## Files

- `lb.go` - Load balancer implementation with health checking
- `be.go` - Simple backend server for testing
- `README.md` - This documentation file

## Building

```bash
# Build the load balancer
go build -o lb.exe lb.go

# Build the backend server
go build -o be.exe be.go
```

## Usage

### Load Balancer Options

```bash
./lb [options]

Options:
  -p, --port <port>                    Listen port (default: 80)
  -b, --backends <host:port>           Backend servers (default: 127.0.0.1:8080 127.0.0.1:8081)
  --health-check-period <seconds>      Health check interval in seconds (default: 10)
  --health-check-url <path>            Health check URL path (default: /)
  --no-health-check                    Disable health checking
  -h, --help                           Show help message
```

### Backend Server Options

```bash
./be [options]

Options:
  -p, --port <port>       Listen port (default: 8080)
  -c, --content <text>    Content identifier (default: 'Backend Server')
  -h, --help             Show help message
```

## Testing Instructions

### 1. Basic Setup

Start multiple backend servers in separate terminals:

```bash
# Terminal 1
./be -p 8080 -c "Server A"

# Terminal 2
./be -p 8081 -c "Server B"

# Terminal 3
./be -p 8082 -c "Server C"
```

Start the load balancer:

```bash
# Terminal 4
./lb -p 80 -b 127.0.0.1:8080 127.0.0.1:8081 127.0.0.1:8082 --health-check-period 5
```

### 2. Test Normal Operation

Make several requests to verify round-robin distribution:

```bash
curl http://localhost
curl http://localhost
curl http://localhost
curl http://localhost
curl http://localhost
curl http://localhost
```

You should see responses from different servers in rotation.

### 3. Test Server Failure

1. Kill one backend server (e.g., press Ctrl+C in Terminal 2)
2. Wait 5-10 seconds for health check to detect failure
3. Make more requests:

```bash
curl http://localhost
curl http://localhost
curl http://localhost
```

Requests should only go to healthy servers (Server A and Server C).

### 4. Test Server Recovery

1. Restart the killed server:
```bash
./be -p 8081 -c "Server B"
```

2. Wait for next health check cycle (5-10 seconds)
3. Make requests - Server B should be back in rotation

### 5. Test Concurrent Load

Create a `urls.txt` file:

```bash
cat > urls.txt << EOF
url = "http://localhost"
url = "http://localhost"
url = "http://localhost"
url = "http://localhost"
url = "http://localhost"
url = "http://localhost"
url = "http://localhost"
url = "http://localhost"
EOF
```

Test with different concurrency levels:

```bash
# 3 concurrent requests
curl --parallel --parallel-immediate --parallel-max 3 --config urls.txt

# 5 concurrent requests
curl --parallel --parallel-immediate --parallel-max 5 --config urls.txt

# 10 concurrent requests
curl --parallel --parallel-immediate --parallel-max 10 --config urls.txt
```

### 6. Test All Servers Down

1. Kill all backend servers
2. Make a request:

```bash
curl http://localhost
```

Should return: `503 Service Unavailable - No servers available`

## Advanced Testing Scenarios

### Fast Health Checks
```bash
./lb -p 80 -b 127.0.0.1:8080 127.0.0.1:8081 127.0.0.1:8082 --health-check-period 3
```

### Custom Health Check Endpoint
```bash
./lb -p 80 -b 127.0.0.1:8080 127.0.0.1:8081 127.0.0.1:8082 --health-check-url /health
```

### Disable Health Checking
```bash
./lb -p 80 -b 127.0.0.1:8080 127.0.0.1:8081 127.0.0.1:8082 --no-health-check
```

### Different Port Configuration
```bash
# Load balancer on port 8000
./lb -p 8000 -b 127.0.0.1:8080 127.0.0.1:8081

# Test with custom port
curl http://localhost:8000
```

## Expected Behavior

### Normal Operation
- Requests distributed evenly across all healthy servers
- Each server should receive roughly equal number of requests
- No client errors or timeouts

### Server Failure
- Failed servers automatically removed from rotation
- No client requests sent to failed servers
- No 502 Bad Gateway errors for new requests
- Existing healthy servers continue to serve requests

### Server Recovery
- Recovered servers automatically added back to rotation
- Load distribution resumes across all healthy servers
- No manual intervention required

### High Load
- Load balancer should handle concurrent requests smoothly
- No connection refused errors under normal load
- Requests distributed fairly even under high concurrency

## Troubleshooting

### Common Issues

1. **Permission denied on port 80**
   - Solution: Use a different port (`-p 8000`) or run with sudo

2. **Backend servers not responding**
   - Check if backend servers are running: `ps aux | grep be`
   - Verify ports are not in use: `netstat -tulpn | grep :8080`

3. **Health checks failing**
   - Check backend server logs for errors
   - Verify health check URL is accessible: `curl http://127.0.0.1:8080/`

4. **Load balancer not distributing evenly**
   - Ensure all backend servers are healthy
   - Check load balancer logs for health check status

### Debugging Tips

- Use `-v` flag with curl for verbose output
- Monitor backend server terminals for request logs
- Check load balancer output for health check status
- Use `netstat` to verify which ports are listening

## Performance Notes

- Health checks run concurrently and don't block request handling
- Each client connection is handled in a separate goroutine
- Round-robin index is protected by mutex for thread safety
- Backend server health status uses read-write mutex for efficient access

## Architecture

```
Client → Load Balancer → Backend Server 1
                    ├─→ Backend Server 2  
                    └─→ Backend Server 3

Health Checker ──→ Periodic checks to all backends
```

The load balancer maintains a list of backend servers and their health status, routing requests only to healthy servers using a round-robin algorithm.
