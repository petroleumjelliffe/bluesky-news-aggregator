package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/spf13/viper"
	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/aggregator"
	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/database"
)

var templates *template.Template

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

	// Load templates
	templates = template.Must(template.ParseGlob("cmd/api/templates/*.html"))

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

	// Static files
	fileServer := http.FileServer(http.Dir("cmd/api/static"))
	s.router.Handle("/static/*", http.StripPrefix("/static/", fileServer))

	// Routes
	s.router.Get("/", s.handleRoot)
	s.router.Get("/api/trending", s.handleTrending)
	s.router.Get("/health", s.handleHealth)
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	data := struct {
		Title string
	}{
		Title: "Bluesky News Aggregator",
	}

	if err := templates.ExecuteTemplate(w, "index.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		log.Printf("Template error: %v", err)
	}
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
