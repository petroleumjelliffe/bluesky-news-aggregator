package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/aggregator"
	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/config"
	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/database"
)

var templates *template.Template

// Server wraps the HTTP server
type Server struct {
	db         *database.DB
	aggregator *aggregator.Aggregator
	router     *chi.Mux
	config     *config.Config
}

// TrendingResponse is the API response for trending links
type TrendingResponse struct {
	Links []LinkResponse `json:"links"`
}

// LinkResponse is a single link in the API response
type LinkResponse struct {
	ID            int                     `json:"id"`
	URL           string                  `json:"url"`
	Title         string                  `json:"title"`
	Description   string                  `json:"description"`
	ImageURL      string                  `json:"image_url"`
	ShareCount    int                     `json:"share_count"`
	LastSharedAt  string                  `json:"last_shared_at"`
	Sharers       []string                `json:"sharers"`
	SharerAvatars []database.SharerAvatar `json:"sharer_avatars"`
}

func main() {
	// Load configuration (supports env vars)
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Load templates
	templates = template.Must(template.ParseGlob("cmd/api/templates/*.html"))

	// Initialize database (log safe connection string without password)
	log.Printf("Connecting to database: %s", cfg.Database.DatabaseConnStringSafe())
	db, err := database.NewDB(cfg.Database.DatabaseConnString())
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	// Create aggregator with default ranking
	agg := aggregator.NewAggregator(db, &aggregator.ShareCountRanking{})

	// Create server
	server := &Server{
		db:         db,
		aggregator: agg,
		router:     chi.NewRouter(),
		config:     cfg,
	}

	server.setupRoutes()

	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)

	// Start server with or without TLS
	if cfg.Server.IsTLSEnabled() {
		log.Printf("Starting HTTPS server on %s", addr)
		if err := http.ListenAndServeTLS(addr, cfg.Server.TLSCertFile, cfg.Server.TLSKeyFile, server.router); err != nil {
			log.Fatalf("Server failed: %v", err)
		}
	} else {
		log.Printf("Starting HTTP server on %s (TLS not configured)", addr)
		if err := http.ListenAndServe(addr, server.router); err != nil {
			log.Fatalf("Server failed: %v", err)
		}
	}
}

func (s *Server) setupRoutes() {
	// Middleware stack (order matters)
	s.router.Use(middleware.RequestID)
	s.router.Use(middleware.RealIP)
	s.router.Use(middleware.Logger)
	s.router.Use(middleware.Recoverer)

	// Security middleware
	s.router.Use(s.securityHeadersMiddleware)
	s.router.Use(s.corsMiddleware)
	s.router.Use(s.rateLimitMiddleware)

	// Static files
	fileServer := http.FileServer(http.Dir("cmd/api/static"))
	s.router.Handle("/static/*", http.StripPrefix("/static/", fileServer))

	// Routes
	s.router.Get("/", s.handleRoot)
	s.router.Get("/api/trending", s.handleTrending)
	s.router.Get("/api/links/{id}/posts", s.handleLinkPosts)
	s.router.Get("/health", s.handleHealth)
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	data := struct {
		Title string
	}{
		Title: "Bluesky News Aggregator",
	}

	if err := templates.ExecuteTemplate(w, "index.html", data); err != nil {
		log.Printf("Template error: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

func (s *Server) handleTrending(w http.ResponseWriter, r *http.Request) {
	// Parse query parameters
	hoursStr := r.URL.Query().Get("hours")
	if hoursStr == "" {
		hoursStr = "24"
	}
	hours, err := strconv.Atoi(hoursStr)
	if err != nil || hours < 1 || hours > 720 {
		http.Error(w, "Invalid hours parameter (1-720)", http.StatusBadRequest)
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

	// Parse degree filter: 0 = all, 1 = 1st-degree only, 2 = 2nd-degree only
	degreeStr := r.URL.Query().Get("degree")
	degree := 0 // Default: all posts
	if degreeStr != "" {
		degree, err = strconv.Atoi(degreeStr)
		if err != nil || degree < 0 || degree > 2 {
			http.Error(w, "Invalid degree parameter (0=all, 1=1st-degree, 2=2nd-degree)", http.StatusBadRequest)
			return
		}
	}

	// Get trending links (filtered by degree if specified)
	var links []database.TrendingLink
	if degree == 0 {
		links, err = s.aggregator.GetTrendingLinks(hours, limit)
	} else {
		links, err = s.aggregator.GetTrendingLinksByDegree(hours, limit, degree)
	}
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
		// Fetch sharer avatars for this link
		sharers, err := s.db.GetLinkSharers(link.ID)
		if err != nil {
			log.Printf("Error getting sharers for link %d: %v", link.ID, err)
			sharers = []database.SharerAvatar{} // Empty on error
		}

		response.Links[i] = LinkResponse{
			ID:            link.ID,
			URL:           link.NormalizedURL,
			Title:         stringOrEmpty(link.Title),
			Description:   stringOrEmpty(link.Description),
			ImageURL:      stringOrEmpty(link.OGImageURL),
			ShareCount:    link.ShareCount,
			LastSharedAt:  link.LastSharedAt.Format("2006-01-02T15:04:05Z"),
			Sharers:       []string(link.Sharers),
			SharerAvatars: sharers,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) handleLinkPosts(w http.ResponseWriter, r *http.Request) {
	// Get link ID from URL parameter
	linkIDStr := chi.URLParam(r, "id")
	linkID, err := strconv.Atoi(linkIDStr)
	if err != nil {
		http.Error(w, "Invalid link ID", http.StatusBadRequest)
		return
	}

	// Get posts for this link
	posts, err := s.db.GetLinkPosts(linkID)
	if err != nil {
		log.Printf("Error getting link posts: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Return posts as JSON
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"link_id": linkID,
		"posts":   posts,
	})
}

// securityHeadersMiddleware adds security headers to all responses
func (s *Server) securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Prevent MIME type sniffing
		w.Header().Set("X-Content-Type-Options", "nosniff")

		// Prevent clickjacking
		w.Header().Set("X-Frame-Options", "DENY")

		// XSS protection (legacy but still useful)
		w.Header().Set("X-XSS-Protection", "1; mode=block")

		// Referrer policy
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")

		// Content Security Policy (adjust as needed for your frontend)
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' https: data:; connect-src 'self'")

		// HSTS (only if TLS is enabled)
		if s.config.Server.IsTLSEnabled() {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}

		next.ServeHTTP(w, r)
	})
}

// corsMiddleware handles CORS with configurable allowed origins
func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := s.config.Server.CORSAllowOrigin

		// If specific origin is configured, validate it
		if origin != "*" {
			// Check if request origin matches allowed origin
			requestOrigin := r.Header.Get("Origin")
			if requestOrigin != "" && requestOrigin != origin {
				// Origin not allowed - don't set CORS headers
				next.ServeHTTP(w, r)
				return
			}
		}

		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Max-Age", "86400") // 24 hours

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// rateLimitMiddleware implements simple IP-based rate limiting
func (s *Server) rateLimitMiddleware(next http.Handler) http.Handler {
	// Simple in-memory rate limiter
	type visitor struct {
		count    int
		lastSeen time.Time
	}

	var (
		visitors = make(map[string]*visitor)
		mu       sync.Mutex
	)

	// Cleanup old entries periodically
	go func() {
		for {
			time.Sleep(time.Minute)
			mu.Lock()
			for ip, v := range visitors {
				if time.Since(v.lastSeen) > time.Minute {
					delete(visitors, ip)
				}
			}
			mu.Unlock()
		}
	}()

	limitPerMinute := s.config.Server.RateLimitRPM
	if limitPerMinute == 0 {
		limitPerMinute = 100 // Default
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip rate limiting for health checks
		if r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}

		ip := r.RemoteAddr
		// Use X-Forwarded-For if behind proxy
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			ip = xff
		}

		mu.Lock()
		v, exists := visitors[ip]
		if !exists {
			visitors[ip] = &visitor{count: 1, lastSeen: time.Now()}
			mu.Unlock()
			next.ServeHTTP(w, r)
			return
		}

		// Reset count if more than a minute has passed
		if time.Since(v.lastSeen) > time.Minute {
			v.count = 1
			v.lastSeen = time.Now()
			mu.Unlock()
			next.ServeHTTP(w, r)
			return
		}

		v.count++
		v.lastSeen = time.Now()

		if v.count > limitPerMinute {
			mu.Unlock()
			w.Header().Set("Retry-After", "60")
			http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		mu.Unlock()

		next.ServeHTTP(w, r)
	})
}

func stringOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
