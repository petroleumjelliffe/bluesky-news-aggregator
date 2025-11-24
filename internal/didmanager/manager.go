package didmanager

import (
	"log"
	"sync"

	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/database"
)

// Manager tracks followed DIDs for filtering Jetstream events
// Supports both 1st-degree (direct follows) and 2nd-degree (extended network)
type Manager struct {
	db              *database.DB
	dids            map[string]int // Map of DID -> degree (1 or 2)
	mu              sync.RWMutex
	include2ndDegree bool
	minSourceCount  int // For 2nd-degree, minimum number of sources
}

// Config holds DIDManager configuration
type Config struct {
	Include2ndDegree bool
	MinSourceCount   int // For 2nd-degree filtering
}

// NewManager creates a new DID manager
func NewManager(db *database.DB) *Manager {
	return &Manager{
		db:              db,
		dids:            make(map[string]int),
		include2ndDegree: false, // Default: only 1st-degree
		minSourceCount:  2,      // Default: require 2+ sources for 2nd-degree
	}
}

// NewManagerWithConfig creates a DID manager with custom configuration
func NewManagerWithConfig(db *database.DB, config *Config) *Manager {
	return &Manager{
		db:              db,
		dids:            make(map[string]int),
		include2ndDegree: config.Include2ndDegree,
		minSourceCount:  config.MinSourceCount,
	}
}

// LoadFromDatabase loads followed DIDs from the database
// This now uses the network_accounts table which supports both 1st and 2nd degree
func (m *Manager) LoadFromDatabase() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Try loading from network_accounts first (new schema)
	networkDIDs, err := m.db.GetAllNetworkDIDs()
	if err == nil && len(networkDIDs) > 0 {
		// Clear existing and rebuild
		m.dids = make(map[string]int)

		firstCount := 0
		secondCount := 0

		for did, degree := range networkDIDs {
			// Always include 1st-degree
			if degree == 1 {
				m.dids[did] = degree
				firstCount++
			}

			// Conditionally include 2nd-degree
			if degree == 2 && m.include2ndDegree {
				m.dids[did] = degree
				secondCount++
			}
		}

		if m.include2ndDegree {
			log.Printf("[INFO] Loaded %d DIDs (%d 1st-degree, %d 2nd-degree)", len(m.dids), firstCount, secondCount)
		} else {
			log.Printf("[INFO] Loaded %d 1st-degree DIDs (2nd-degree filtering disabled)", firstCount)
		}

		return nil
	}

	// Fallback: Try loading from old follows table for backwards compatibility
	follows, err := m.db.GetAllFollows()
	if err != nil {
		return err
	}

	// Clear existing and rebuild
	m.dids = make(map[string]int)
	for _, follow := range follows {
		m.dids[follow.DID] = 1 // All are 1st-degree in old schema
	}

	log.Printf("[INFO] Loaded %d followed DIDs (from legacy follows table)", len(m.dids))
	return nil
}

// IsFollowed checks if a DID is in the followed set
func (m *Manager) IsFollowed(did string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, exists := m.dids[did]
	return exists
}

// GetDegree returns the degree of a DID (1 or 2), or 0 if not followed
func (m *Manager) GetDegree(did string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.dids[did]
}

// GetDIDs returns a slice of all followed DIDs (for Jetstream filter)
func (m *Manager) GetDIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	dids := make([]string, 0, len(m.dids))
	for did := range m.dids {
		dids = append(dids, did)
	}
	return dids
}

// GetDIDsByDegree returns DIDs filtered by degree
func (m *Manager) GetDIDsByDegree(degree int) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	dids := make([]string, 0)
	for did, d := range m.dids {
		if d == degree {
			dids = append(dids, did)
		}
	}
	return dids
}

// AddDID adds a DID to the followed set with a degree
func (m *Manager) AddDID(did string, degree int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dids[did] = degree
}

// RemoveDID removes a DID from the followed set
func (m *Manager) RemoveDID(did string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.dids, did)
}

// Count returns the number of followed DIDs
func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.dids)
}

// CountByDegree returns counts broken down by degree
func (m *Manager) CountByDegree() map[int]int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	counts := make(map[int]int)
	for _, degree := range m.dids {
		counts[degree]++
	}
	return counts
}

// SetInclude2ndDegree enables or disables 2nd-degree filtering
func (m *Manager) SetInclude2ndDegree(include bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.include2ndDegree = include
}

// IsIncluding2ndDegree returns whether 2nd-degree filtering is enabled
func (m *Manager) IsIncluding2ndDegree() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.include2ndDegree
}
