package aggregator

import (
	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/database"
)

// RankingStrategy defines how links should be ranked
type RankingStrategy interface {
	Rank(links []database.TrendingLink) []database.TrendingLink
}

// ShareCountRanking ranks links by share count (default)
type ShareCountRanking struct{}

// Rank sorts links by share count, then by recency
func (r *ShareCountRanking) Rank(links []database.TrendingLink) []database.TrendingLink {
	// Already sorted by share_count DESC in the SQL query
	// This is a pass-through for now, but can be extended
	return links
}

// RecencyWeightedRanking ranks links with a recency boost
// TODO: Implement this in the future
type RecencyWeightedRanking struct{}

func (r *RecencyWeightedRanking) Rank(links []database.TrendingLink) []database.TrendingLink {
	// TODO: Implement recency-weighted ranking
	// Formula: score = share_count * (1 + recency_factor)
	return links
}

// VelocityRanking ranks links by how quickly they're gaining shares
// TODO: Implement this in the future
type VelocityRanking struct{}

func (r *VelocityRanking) Rank(links []database.TrendingLink) []database.TrendingLink {
	// TODO: Implement velocity-based ranking
	// Requires tracking share rate over time
	return links
}

// Aggregator handles link aggregation and ranking
type Aggregator struct {
	db      *database.DB
	ranker  RankingStrategy
}

// NewAggregator creates a new aggregator with the given ranking strategy
func NewAggregator(db *database.DB, ranker RankingStrategy) *Aggregator {
	if ranker == nil {
		ranker = &ShareCountRanking{} // Default
	}

	return &Aggregator{
		db:     db,
		ranker: ranker,
	}
}

// GetTrendingLinks retrieves and ranks trending links
func (a *Aggregator) GetTrendingLinks(hoursBack, limit int) ([]database.TrendingLink, error) {
	links, err := a.db.GetTrendingLinks(hoursBack, limit)
	if err != nil {
		return nil, err
	}

	// Apply ranking strategy
	return a.ranker.Rank(links), nil
}

// GetTrendingLinksByDegree retrieves and ranks trending links filtered by network degree
// degree: 0 = all posts, 1 = 1st-degree only, 2 = 2nd-degree only
func (a *Aggregator) GetTrendingLinksByDegree(hoursBack, limit, degree int) ([]database.TrendingLink, error) {
	links, err := a.db.GetTrendingLinksByDegree(hoursBack, limit, degree)
	if err != nil {
		return nil, err
	}

	// Apply ranking strategy
	return a.ranker.Rank(links), nil
}
