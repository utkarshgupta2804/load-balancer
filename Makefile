# Makefile for Load Balancer Project

# Go compiler
GO = go

# Build targets
all: lb be

lb: lb.go
	$(GO) build -o lb lb.go

be: be.go
	$(GO) build -o be be.go

# Clean build artifacts
clean:
	rm -f lb be

# Test the setup
test: all
	@echo "Starting backend server in background..."
	./be &
	@sleep 2
	@echo "Starting load balancer in background..."
	./lb &
	@sleep 2
	@echo "Testing with curl..."
	curl http://localhost/ || true
	@echo "Stopping processes..."
	@pkill -f "./lb" || true
	@pkill -f "./be" || true

# Install dependencies (if any)
deps:
	$(GO) mod tidy

# Format code
fmt:
	$(GO) fmt ./...

# Run tests
check:
	$(GO) vet ./...

.PHONY: all clean test deps fmt check