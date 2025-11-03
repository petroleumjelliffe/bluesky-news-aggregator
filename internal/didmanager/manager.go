package didmanager

import (
	"log"
	"sync"

	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/database"
)

// Manager tracks followed DIDs for filtering Jetstream events
type Manager struct {
	db   *database.DB
	dids map[string]bool // Set of followed DIDs
	mu   sync.RWMutex
}

// NewManager creates a new DID manager
func NewManager(db *database.DB) *Manager {
	return &Manager{
		db:   db,
		dids: make(map[string]bool),
	}
}

// LoadFromDatabase loads followed DIDs from the database
func (m *Manager) LoadFromDatabase() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	follows, err := m.db.GetAllFollows()
	if err != nil {
		return err
	}

	// Clear existing and rebuild
	m.dids = make(map[string]bool)
	for _, follow := range follows {
		m.dids[follow.DID] = true
	}

	log.Printf("[INFO] Loaded %d followed DIDs", len(m.dids))
	return nil
}

// IsFollowed checks if a DID is in the followed set
func (m *Manager) IsFollowed(did string) bool {
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

// AddDID adds a DID to the followed set
func (m *Manager) AddDID(did string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dids[did] = true
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
