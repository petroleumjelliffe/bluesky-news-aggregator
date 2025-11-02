# Quick Start Guide

## Prerequisites

1. **Install Go 1.21+**
   ```bash
   # Check your Go version
   go version
   ```

2. **Install PostgreSQL**
   ```bash
   # macOS
   brew install postgresql@14
   brew services start postgresql@14

   # Ubuntu/Debian
   sudo apt-get install postgresql-14

   # Windows
   # Download from https://www.postgresql.org/download/windows/
   ```

3. **Create a Bluesky App Password**
   - Go to https://bsky.app/settings/app-passwords
   - Create a new app password
   - Save it for the config file

## Setup Steps

### 1. Extract the zip file
```bash
unzip bluesky-news-aggregator.zip
cd bluesky-news-aggregator
```

### 2. Install Go dependencies
```bash
go mod download
```

### 3. Set up the database
```bash
# Create the database
createdb bluesky_news

# Or using psql
psql -U postgres -c "CREATE DATABASE bluesky_news;"
```

### 4. Configure the application
```bash
# Copy the example config
cp config/config.example.yaml config/config.yaml

# Edit config/config.yaml with your settings:
# - Database credentials
# - Your Bluesky handle and app password
```

Example `config/config.yaml`:
```yaml
database:
  host: localhost
  port: 5432
  user: postgres
  password: yourpassword
  dbname: bluesky_news
  sslmode: disable

bluesky:
  handle: yourname.bsky.social
  password: xxxx-xxxx-xxxx-xxxx  # App password from Bluesky

server:
  port: 8080
  host: 0.0.0.0

polling:
  interval_minutes: 15
  max_concurrent: 10
  rate_limit_ms: 100

aggregation:
  default_hours: 24
  max_results: 100
```

### 5. Run migrations
```bash
go run cmd/migrate/main.go
```

### 6. Start the poller (in one terminal)
```bash
go run cmd/poller/main.go
```

This will:
- Fetch your follows list from Bluesky
- Poll each account for recent posts every 15 minutes
- Extract URLs and fetch OpenGraph metadata
- Store everything in the database

### 7. Start the API server (in another terminal)
```bash
go run cmd/api/main.go
```

### 8. View the results
Open your browser to http://localhost:8080

You should see a simple web page with trending links!

## API Usage

### Get trending links
```bash
# Last 24 hours, top 50 links
curl http://localhost:8080/api/trending?hours=24&limit=50

# Last 6 hours, top 20 links
curl http://localhost:8080/api/trending?hours=6&limit=20
```

### Health check
```bash
curl http://localhost:8080/health
```

## Building for Production

```bash
# Build binaries
make build

# This creates:
# - bin/poller
# - bin/api
# - bin/migrate

# Run the poller
./bin/poller

# Run the API server
./bin/api
```

## Troubleshooting

### "Failed to connect to database"
- Check that PostgreSQL is running: `pg_isready`
- Verify database exists: `psql -l | grep bluesky_news`
- Check credentials in `config/config.yaml`

### "Authentication failed"
- Verify your Bluesky handle (should be like `yourname.bsky.social`)
- Make sure you're using an app password, not your main password
- Generate a new app password at https://bsky.app/settings/app-passwords

### "No links showing up"
- Wait for the poller to run (check logs)
- Make sure the accounts you follow have posted links recently
- Check database: `psql bluesky_news -c "SELECT COUNT(*) FROM posts;"`

### Import errors
- Run `go mod tidy` to fix dependencies
- Make sure you're in the project root directory

## Next Steps

1. **Add more ranking strategies**: Edit `internal/aggregator/aggregator.go`
2. **Customize the UI**: Edit `cmd/api/main.go` handleRoot function
3. **Add authentication**: For multi-user support
4. **Deploy**: Use Docker, systemd, or cloud services

## Development Tips

- Use `make fmt` to format code
- Use `make test` to run tests (once you write them!)
- Use `make db-reset` to reset the database

Have fun building your Bluesky news aggregator! 🦋
