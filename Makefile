.PHONY: build run-poller run-api migrate clean test start stop restart status \
        backfill-recent backfill-all migrate-follows cleanup cleanup-stats avatar-stats \
        logs-firehose logs-api deps fmt lint db-create db-drop db-reset

# Build all binaries
build:
	@echo "Building all binaries..."
	@mkdir -p bin logs
	go build -o bin/poller cmd/poller/main.go
	go build -o bin/api cmd/api/main.go
	go build -o bin/migrate cmd/migrate/main.go
	go build -o bin/firehose cmd/firehose/main.go
	go build -o bin/backfill cmd/backfill/main.go
	go build -o bin/metadata-fetcher cmd/metadata-fetcher/main.go
	go build -o bin/migrate-follows cmd/migrate-follows/main.go
	go build -o bin/janitor cmd/janitor/main.go
	@echo "✓ Build complete"

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

# Service management commands
start: build
	@echo "Starting services..."
	@killall firehose api 2>/dev/null || true
	@sleep 1
	@./bin/firehose >> logs/firehose.log 2>&1 &
	@echo "✓ Firehose started (PID: $$!)"
	@./bin/api >> logs/api.log 2>&1 &
	@echo "✓ API started (PID: $$!)"
	@echo "Run 'make status' to check services"

stop:
	@echo "Stopping services..."
	@killall firehose 2>/dev/null && echo "✓ Firehose stopped" || echo "- Firehose not running"
	@killall api 2>/dev/null && echo "✓ API stopped" || echo "- API not running"

restart: stop
	@sleep 2
	@make start

status:
	@echo "=== Service Status ==="
	@echo ""
	@echo "Firehose:"
	@ps aux | grep "[.]bin/firehose" | awk '{print "  PID: " $$2 ", CPU: " $$3 "%, MEM: " $$4 "%"}' || echo "  Not running"
	@echo ""
	@echo "API:"
	@ps aux | grep "[.]bin/api" | awk '{print "  PID: " $$2 ", CPU: " $$3 "%, MEM: " $$4 "%"}' || echo "  Not running"
	@echo ""
	@echo "Recent Activity (last 5 minutes):"
	@psql -d bluesky_news -t -c "SELECT COUNT(*) || ' posts' FROM posts WHERE created_at > NOW() - INTERVAL '5 minutes';"
	@psql -d bluesky_news -t -c "SELECT COUNT(*) || ' links' FROM links WHERE first_seen_at > NOW() - INTERVAL '5 minutes';"
	@echo ""
	@echo "Database Stats:"
	@psql -d bluesky_news -t -c "SELECT COUNT(*) || ' total posts' FROM posts;"
	@psql -d bluesky_news -t -c "SELECT COUNT(*) || ' total links' FROM links;"
	@psql -d bluesky_news -t -c "SELECT COUNT(*) || ' links with metadata' FROM links WHERE title IS NOT NULL;"

logs-firehose:
	@tail -f logs/firehose.log

logs-api:
	@tail -f logs/api.log

# Backfill management
backfill-recent:
	@echo "Running backfill for accounts active in last 24 hours..."
	@psql -d bluesky_news -c "UPDATE follows SET backfill_completed = false WHERE last_seen_at > NOW() - INTERVAL '24 hours';"
	@./bin/backfill

backfill-all:
	@echo "Running full backfill for all accounts..."
	@psql -d bluesky_news -c "UPDATE follows SET backfill_completed = false;"
	@./bin/backfill

# Follow migration (fetches DIDs and avatars from Bluesky API)
migrate-follows:
	@echo "Migrating follows from Bluesky API..."
	@./bin/migrate-follows

# Manual cleanup (runs janitor once)
# NOTE: Cleanup now runs automatically on firehose startup and hourly
cleanup:
	@echo "Running manual cleanup..."
	@./bin/janitor

# Database cleanup stats
cleanup-stats:
	@echo "=== Cleanup Stats ==="
	@echo ""
	@echo "Data Age Distribution:"
	@psql -d bluesky_news -t -c "SELECT '  < 1h: ' || COUNT(*) FROM posts WHERE created_at > NOW() - INTERVAL '1 hour';"
	@psql -d bluesky_news -t -c "SELECT '  1-6h: ' || COUNT(*) FROM posts WHERE created_at BETWEEN NOW() - INTERVAL '6 hours' AND NOW() - INTERVAL '1 hour';"
	@psql -d bluesky_news -t -c "SELECT '  6-24h: ' || COUNT(*) FROM posts WHERE created_at BETWEEN NOW() - INTERVAL '24 hours' AND NOW() - INTERVAL '6 hours';"
	@psql -d bluesky_news -t -c "SELECT '  > 24h: ' || COUNT(*) FROM posts WHERE created_at < NOW() - INTERVAL '24 hours';"
	@echo ""
	@echo "Trending Links (5+ shares, kept forever):"
	@psql -d bluesky_news -t -c "SELECT COUNT(*) || ' links' FROM links l WHERE (SELECT COUNT(*) FROM post_links pl WHERE pl.link_id = l.id) >= 5;"
	@echo ""
	@echo "Cursor Status:"
	@psql -d bluesky_news -t -c "SELECT 'Last update: ' || last_updated || ', Age: ' || EXTRACT(EPOCH FROM NOW() - last_updated)::int || 's' FROM jetstream_state WHERE id = 1;"

# Avatar stats
avatar-stats:
	@echo "=== Avatar Stats ==="
	@psql -d bluesky_news -t -c "SELECT 'Total follows: ' || COUNT(*) FROM follows;"
	@psql -d bluesky_news -t -c "SELECT 'With avatars: ' || COUNT(*) FROM follows WHERE avatar_url IS NOT NULL;"
	@psql -d bluesky_news -t -c "SELECT 'Coverage: ' || ROUND(100.0 * COUNT(avatar_url) / COUNT(*), 1) || '%' FROM follows;"
