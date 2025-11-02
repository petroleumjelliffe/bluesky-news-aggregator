package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/spf13/viper"
	"github.com/yourusername/bluesky-news-aggregator/internal/aggregator"
	"github.com/yourusername/bluesky-news-aggregator/internal/database"
)

// Config holds application configuration
type Config struct {
	DatabaseURL string
	ServerHost  string
	ServerPort  int
}

// Server wraps the HTTP server
type Server struct {
	aggregator *aggregator.Aggregator
	router     *chi.Mux
}

// TrendingResponse is the API response for trending links
type TrendingResponse struct {
	Links []LinkResponse `json:"links"`
}

// LinkResponse is a single link in the API response
type LinkResponse struct {
	ID           int      `json:"id"`
	URL          string   `json:"url"`
	Title        string   `json:"title"`
	Description  string   `json:"description"`
	ImageURL     string   `json:"image_url"`
	ShareCount   int      `json:"share_count"`
	LastSharedAt string   `json:"last_shared_at"`
	Sharers      []string `json:"sharers"`
}

func main() {
	// Load configuration
	config, err := loadConfig()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Initialize database
	db, err := database.NewDB(config.DatabaseURL)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	// Create aggregator with default ranking
	agg := aggregator.NewAggregator(db, &aggregator.ShareCountRanking{})

	// Create server
	server := &Server{
		aggregator: agg,
		router:     chi.NewRouter(),
	}

	server.setupRoutes()

	addr := fmt.Sprintf("%s:%d", config.ServerHost, config.ServerPort)
	log.Printf("Starting API server on %s", addr)

	if err := http.ListenAndServe(addr, server.router); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

func loadConfig() (*Config, error) {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath("./config")
	viper.AddConfigPath(".")

	if err := viper.ReadInConfig(); err != nil {
		return nil, err
	}

	dbURL := fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		viper.GetString("database.host"),
		viper.GetInt("database.port"),
		viper.GetString("database.user"),
		viper.GetString("database.password"),
		viper.GetString("database.dbname"),
		viper.GetString("database.sslmode"),
	)

	return &Config{
		DatabaseURL: dbURL,
		ServerHost:  viper.GetString("server.host"),
		ServerPort:  viper.GetInt("server.port"),
	}, nil
}

func (s *Server) setupRoutes() {
	// Middleware
	s.router.Use(middleware.Logger)
	s.router.Use(middleware.Recoverer)
	s.router.Use(middleware.RequestID)
	s.router.Use(corsMiddleware)

	// Routes
	s.router.Get("/", s.handleRoot)
	s.router.Get("/api/trending", s.handleTrending)
	s.router.Get("/health", s.handleHealth)
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	html := `
<!DOCTYPE html>
<html>
<head>
    <title>Bluesky News Aggregator</title>
    <style>
        body { font-family: sans-serif; max-width: 800px; margin: 50px auto; padding: 20px; }
        h1 { color: #333; }
        .link-card { border: 1px solid #ddd; padding: 15px; margin: 15px 0; border-radius: 8px; }
        .link-card img { max-width: 200px; height: auto; }
        .share-count { color: #666; font-size: 0.9em; }
        .sharers { color: #999; font-size: 0.8em; }
    </style>
</head>
<body>
    <h1>Bluesky News Aggregator</h1>
    <p>API Endpoints:</p>
    <ul>
        <li><code>GET /api/trending?hours=24&limit=50</code> - Get trending links</li>
        <li><code>GET /health</code> - Health check</li>
    </ul>
    <div id="links"></div>
    <script>
        fetch('/api/trending?hours=24&limit=20')
            .then(res => res.json())
            .then(data => {
                const container = document.getElementById('links');
                container.innerHTML = '<h2>Trending in Last 24 Hours</h2>';
                data.links.forEach(link => {
                    const card = document.createElement('div');
                    card.className = 'link-card';
                    card.innerHTML = \`
                        \${link.image_url ? \`<img src="\${link.image_url}" alt=""\>\` : ''}
                        <h3><a href="\${link.url}" target="_blank">\${link.title || link.url}</a></h3>
                        <p>\${link.description || ''}</p>
                        <div class="share-count">Shared by \${link.share_count} account(s)</div>
                        <div class="sharers">\${link.sharers.slice(0, 5).join(', ')}\${link.sharers.length > 5 ? '...' : ''}</div>
                    \`;
                    container.appendChild(card);
                });
            });
    </script>
</body>
</html>
`
	w.Write([]byte(html))
}

func (s *Server) handleTrending(w http.ResponseWriter, r *http.Request) {
	// Parse query parameters
	hoursStr := r.URL.Query().Get("hours")
	if hoursStr == "" {
		hoursStr = "24"
	}
	hours, err := strconv.Atoi(hoursStr)
	if err != nil || hours < 1 || hours > 168 {
		http.Error(w, "Invalid hours parameter (1-168)", http.StatusBadRequest)
		return
	}

	limitStr := r.URL.Query().Get("limit")
	if limitStr == "" {
		limitStr = "50"
	}
	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit < 1 || limit > 100 {
		http.Error(w, "Invalid limit parameter (1-100)", http.StatusBadRequest)
		return
	}

	// Get trending links
	links, err := s.aggregator.GetTrendingLinks(hours, limit)
	if err != nil {
		log.Printf("Error getting trending links: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Convert to response format
	response := TrendingResponse{
		Links: make([]LinkResponse, len(links)),
	}

	for i, link := range links {
		response.Links[i] = LinkResponse{
			ID:           link.ID,
			URL:          link.NormalizedURL,
			Title:        stringOrEmpty(link.Title),
			Description:  stringOrEmpty(link.Description),
			ImageURL:     stringOrEmpty(link.OGImageURL),
			ShareCount:   link.ShareCount,
			LastSharedAt: link.LastSharedAt.Format("2006-01-02T15:04:05Z"),
			Sharers:      link.Sharers,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func stringOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
