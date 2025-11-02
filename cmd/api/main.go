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
	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/aggregator"
	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/database"
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

	// Build connection string, handling empty password
	password := viper.GetString("database.password")
	var dbURL string
	if password == "" {
		dbURL = fmt.Sprintf(
			"host=%s port=%d user=%s dbname=%s sslmode=%s",
			viper.GetString("database.host"),
			viper.GetInt("database.port"),
			viper.GetString("database.user"),
			viper.GetString("database.dbname"),
			viper.GetString("database.sslmode"),
		)
	} else {
		dbURL = fmt.Sprintf(
			"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
			viper.GetString("database.host"),
			viper.GetInt("database.port"),
			viper.GetString("database.user"),
			password,
			viper.GetString("database.dbname"),
			viper.GetString("database.sslmode"),
		)
	}

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
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Bluesky News Aggregator</title>
    <style>
        * { box-sizing: border-box; margin: 0; padding: 0; }
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Oxygen, Ubuntu, Cantarell, sans-serif;
            background: #f5f5f5;
            color: #333;
            line-height: 1.6;
        }
        .container {
            max-width: 900px;
            margin: 0 auto;
            padding: 20px;
        }
        header {
            background: white;
            padding: 30px;
            border-radius: 12px;
            margin-bottom: 30px;
            box-shadow: 0 2px 8px rgba(0,0,0,0.1);
        }
        h1 {
            color: #1a73e8;
            margin-bottom: 10px;
            font-size: 2em;
        }
        .subtitle {
            color: #666;
            font-size: 0.95em;
        }
        .controls {
            background: white;
            padding: 20px;
            border-radius: 12px;
            margin-bottom: 20px;
            box-shadow: 0 2px 8px rgba(0,0,0,0.1);
            display: flex;
            gap: 20px;
            flex-wrap: wrap;
            align-items: center;
        }
        .control-group {
            display: flex;
            align-items: center;
            gap: 10px;
        }
        label {
            font-weight: 500;
            color: #555;
        }
        select, input {
            padding: 8px 12px;
            border: 1px solid #ddd;
            border-radius: 6px;
            font-size: 14px;
            background: white;
        }
        select:focus, input:focus {
            outline: none;
            border-color: #1a73e8;
        }
        button {
            background: #1a73e8;
            color: white;
            border: none;
            padding: 10px 20px;
            border-radius: 6px;
            font-size: 14px;
            font-weight: 500;
            cursor: pointer;
            transition: background 0.2s;
        }
        button:hover {
            background: #1557b0;
        }
        button:active {
            transform: translateY(1px);
        }
        .loading {
            text-align: center;
            padding: 40px;
            color: #999;
        }
        .error {
            background: #fee;
            color: #c33;
            padding: 20px;
            border-radius: 8px;
            margin-bottom: 20px;
        }
        #links {
            display: grid;
            gap: 20px;
        }
        .link-card {
            background: white;
            border-radius: 12px;
            overflow: hidden;
            box-shadow: 0 2px 8px rgba(0,0,0,0.1);
            transition: transform 0.2s, box-shadow 0.2s;
            display: flex;
            gap: 20px;
        }
        .link-card:hover {
            transform: translateY(-2px);
            box-shadow: 0 4px 16px rgba(0,0,0,0.15);
        }
        .link-image {
            flex-shrink: 0;
            width: 240px;
            height: 180px;
            overflow: hidden;
            background: #eee;
        }
        .link-image img {
            width: 100%;
            height: 100%;
            object-fit: cover;
        }
        .link-content {
            flex: 1;
            padding: 20px;
            min-width: 0;
        }
        .link-card h3 {
            margin: 0 0 10px 0;
            font-size: 1.3em;
            line-height: 1.3;
        }
        .link-card h3 a {
            color: #1a73e8;
            text-decoration: none;
        }
        .link-card h3 a:hover {
            text-decoration: underline;
        }
        .link-description {
            color: #666;
            margin-bottom: 15px;
            line-height: 1.5;
        }
        .link-meta {
            display: flex;
            gap: 15px;
            align-items: center;
            font-size: 0.9em;
            color: #777;
            flex-wrap: wrap;
        }
        .share-count {
            font-weight: 600;
            color: #1a73e8;
        }
        .sharers {
            color: #999;
            font-size: 0.85em;
        }
        @media (max-width: 768px) {
            .link-card {
                flex-direction: column;
            }
            .link-image {
                width: 100%;
                height: 200px;
            }
            .controls {
                flex-direction: column;
                align-items: stretch;
            }
            .control-group {
                flex-direction: column;
                align-items: stretch;
            }
        }
    </style>
</head>
<body>
    <div class="container">
        <header>
            <h1>Bluesky News Aggregator</h1>
            <p class="subtitle">Discover the most-shared links from your Bluesky network</p>
        </header>

        <div class="controls">
            <div class="control-group">
                <label for="hours">Time Range:</label>
                <select id="hours">
                    <option value="1">Last Hour</option>
                    <option value="6">Last 6 Hours</option>
                    <option value="24" selected>Last 24 Hours</option>
                    <option value="48">Last 2 Days</option>
                    <option value="72">Last 3 Days</option>
                    <option value="168">Last Week</option>
                </select>
            </div>
            <div class="control-group">
                <label for="limit">Show:</label>
                <select id="limit">
                    <option value="10">10 links</option>
                    <option value="20" selected>20 links</option>
                    <option value="50">50 links</option>
                    <option value="100">100 links</option>
                </select>
            </div>
            <button onclick="loadTrending()">Refresh</button>
        </div>

        <div id="links"></div>
    </div>

    <script>
        function loadTrending() {
            const hours = document.getElementById('hours').value;
            const limit = document.getElementById('limit').value;
            const container = document.getElementById('links');

            container.innerHTML = '<div class="loading">Loading trending links...</div>';

            fetch(` + "`" + `/api/trending?hours=${hours}&limit=${limit}` + "`" + `)
                .then(res => {
                    if (!res.ok) throw new Error('Failed to fetch trending links');
                    return res.json();
                })
                .then(data => {
                    if (!data.links || data.links.length === 0) {
                        container.innerHTML = '<div class="loading">No trending links found. The poller may still be collecting data.</div>';
                        return;
                    }

                    container.innerHTML = '';
                    data.links.forEach(link => {
                        const card = document.createElement('div');
                        card.className = 'link-card';

                        card.innerHTML = ` + "`" + `
                            ${link.image_url ? ` + "`" + `
                                <div class="link-image">
                                    <img src="${link.image_url}" alt="${link.title || 'Link preview'}" onerror="this.parentElement.style.display='none'">
                                </div>
                            ` + "`" + ` : ''}
                            <div class="link-content">
                                <h3><a href="${link.url}" target="_blank" rel="noopener noreferrer">${link.title || link.url}</a></h3>
                                ${link.description ? ` + "`" + `<p class="link-description">${link.description}</p>` + "`" + ` : ''}
                                <div class="link-meta">
                                    <span class="share-count">★ ${link.share_count} share${link.share_count !== 1 ? 's' : ''}</span>
                                    <span class="sharers">Shared by: ${link.sharers.slice(0, 3).join(', ')}${link.sharers.length > 3 ? ` + "`" + ` and ${link.sharers.length - 3} more` + "`" + ` : ''}</span>
                                </div>
                            </div>
                        ` + "`" + `;

                        container.appendChild(card);
                    });
                })
                .catch(err => {
                    container.innerHTML = ` + "`" + `<div class="error">Error: ${err.message}</div>` + "`" + `;
                });
        }

        // Load on page load
        loadTrending();

        // Allow Enter key to refresh
        document.addEventListener('keypress', (e) => {
            if (e.key === 'Enter') loadTrending();
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
			Sharers:      []string(link.Sharers),
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
