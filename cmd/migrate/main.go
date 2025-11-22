package main

import (
	"database/sql"
	"log"
	"os"
	"path/filepath"

	_ "github.com/lib/pq"
	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/config"
)

func main() {
	// Load configuration (supports env vars)
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Connect to database (log safe connection string without password)
	log.Printf("Connecting to database: %s", cfg.Database.DatabaseConnStringSafe())
	db, err := sql.Open("postgres", cfg.Database.DatabaseConnString())
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		log.Fatalf("Failed to ping database: %v", err)
	}

	// Run migrations
	log.Println("Running migrations...")

	migrations, err := filepath.Glob("migrations/*.sql")
	if err != nil {
		log.Fatalf("Failed to find migrations: %v", err)
	}

	for _, migration := range migrations {
		log.Printf("Running migration: %s", filepath.Base(migration))

		content, err := os.ReadFile(migration)
		if err != nil {
			log.Fatalf("Failed to read migration %s: %v", migration, err)
		}

		if _, err := db.Exec(string(content)); err != nil {
			log.Fatalf("Failed to execute migration %s: %v", migration, err)
		}
	}

	log.Println("Migrations completed successfully!")
}
