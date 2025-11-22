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

## Deployment

### Option 1: Render + GitHub Pages (Recommended)

**Frontend (GitHub Pages):**
1. Enable GitHub Pages in repository settings
2. Set source to "Deploy from branch" → `main` → `/docs`
3. Update `docs/js/config.js` with your Render API URL

**Backend (Render):**
1. Create a Render account at https://render.com
2. Click "New" → "Blueprint" and connect this repository
3. Render will use `render.yaml` to create all services
4. Set environment variables in Render dashboard:
   - `BLUESKY_HANDLE`: your.handle.bsky.social
   - `BLUESKY_PASSWORD`: your app password (from https://bsky.app/settings/app-passwords)
   - `CORS_ALLOW_ORIGIN`: https://yourusername.github.io

**Estimated Cost:** ~$21/month (API $7 + Worker $7 + Database $7)

### Option 2: Local Development

See [Setup](#setup) section above.

### Environment Variables

For production, use environment variables instead of config.yaml:

```bash
# Database
DB_HOST=localhost
DB_PORT=5432
DB_USER=postgres
DB_PASSWORD=your-password
DB_NAME=bluesky_news
DB_SSLMODE=require  # Use 'require' in production

# Bluesky
BLUESKY_HANDLE=your.handle.bsky.social
BLUESKY_PASSWORD=your-app-password

# Server
SERVER_HOST=0.0.0.0
SERVER_PORT=8080
CORS_ALLOW_ORIGIN=https://your-domain.com
RATE_LIMIT_RPM=100
```

See `.env.example` for the complete list.

## TODO

- [ ] Add authentication for multi-user support
- [ ] Implement additional ranking strategies (recency-weighted, velocity)
- [ ] Add caching layer (Redis)
- [x] Build frontend UI
- [ ] Add tests
- [x] Render deployment
- [ ] Support for custom lists beyond follows

## License

MIT
