.PHONY: build run-poller run-api migrate clean test

# Build all binaries
build:
	go build -o bin/poller cmd/poller/main.go
	go build -o bin/api cmd/api/main.go
	go build -o bin/migrate cmd/migrate/main.go

# Run the poller
run-poller:
	go run cmd/poller/main.go

# Run the API server
run-api:
	go run cmd/api/main.go

# Run database migrations
migrate:
	go run cmd/migrate/main.go

# Run tests
test:
	go test -v ./...

# Clean build artifacts
clean:
	rm -rf bin/

# Install dependencies
deps:
	go mod download
	go mod tidy

# Format code
fmt:
	go fmt ./...

# Run linter
lint:
	golangci-lint run

# Development database setup
db-create:
	createdb bluesky_news

db-drop:
	dropdb bluesky_news

db-reset: db-drop db-create migrate
