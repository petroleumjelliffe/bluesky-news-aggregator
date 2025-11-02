# Bluesky News Aggregator

A news aggregator that surfaces the most-shared links from your Bluesky network, similar to News.me and Nuzzle.

## Features

- Polls posts from accounts you follow on Bluesky
- Extracts and normalizes shared URLs
- Aggregates links by share count
- Fetches OpenGraph metadata (title, description, image)
- Configurable time windows (last 1-24 hours)
- Modular ranking system (currently by share count)

## Architecture

- **Poller**: Background service that polls Bluesky feeds every 15 minutes
- **API**: REST API for querying trending links
- **Database**: PostgreSQL for storing posts, links, and aggregations

## Prerequisites

- Go 1.21+
- PostgreSQL 14+
- Bluesky account with app password

## Setup

### 1. Install Dependencies

```bash
go mod download
```

### 2. Set up PostgreSQL

```bash
createdb bluesky_news
psql bluesky_news < migrations/001_initial.sql
```

### 3. Configure

Copy `config/config.example.yaml` to `config/config.yaml` and fill in your credentials:

```yaml
database:
  host: localhost
  port: 5432
  user: postgres
  password: yourpassword
  dbname: bluesky_news

bluesky:
  handle: your.handle.bsky.social
  password: your-app-password  # Generate at https://bsky.app/settings/app-passwords

server:
  port: 8080
```

### 4. Run the Poller

```bash
go run cmd/poller/main.go
```

### 5. Run the API Server

```bash
go run cmd/api/main.go
```

## API Endpoints

### Get Trending Links

```
GET /api/trending?hours=24&limit=50
```

Query parameters:
- `hours` (default: 24): Time window in hours
- `limit` (default: 50): Maximum number of results

Response:
```json
{
  "links": [
    {
      "id": 1,
      "url": "https://example.com/article",
      "title": "Article Title",
      "description": "Article description",
      "image_url": "https://example.com/image.jpg",
      "share_count": 15,
      "last_shared_at": "2025-11-02T10:30:00Z",
      "sharers": ["alice.bsky.social", "bob.bsky.social"]
    }
  ]
}
```

## Development

### Run migrations

```bash
go run cmd/migrate/main.go
```

### Build

```bash
make build
```

### Run tests

```bash
go test ./...
```

## Project Structure

```
.
├── cmd/                    # Main applications
│   ├── poller/            # Background polling service
│   ├── api/               # Web API server
│   └── migrate/           # Database migrations
├── internal/              # Private application code
│   ├── aggregator/        # Link aggregation logic
│   ├── bluesky/          # Bluesky API client
│   ├── database/         # Database layer
│   ├── scraper/          # OpenGraph scraper
│   └── urlutil/          # URL utilities
├── migrations/            # SQL migrations
└── config/               # Configuration files
```

## TODO

- [ ] Add authentication for multi-user support
- [ ] Implement additional ranking strategies (recency-weighted, velocity)
- [ ] Add caching layer (Redis)
- [ ] Build frontend UI
- [ ] Add tests
- [ ] Docker deployment
- [ ] Support for custom lists beyond follows

## License

MIT
