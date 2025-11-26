package embeddings

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Provider represents an embedding provider (OpenAI, local, etc.)
type Provider interface {
	GenerateEmbedding(text string) ([]float32, error)
	Dimensions() int
}

// OpenAIProvider implements embedding generation using OpenAI's API
type OpenAIProvider struct {
	apiKey     string
	model      string
	httpClient *http.Client
	dimensions int
}

// NewOpenAIProvider creates a new OpenAI embedding provider
func NewOpenAIProvider(apiKey, model string) *OpenAIProvider {
	if model == "" {
		model = "text-embedding-3-small" // Default to small model (1536 dims)
	}

	dims := 1536 // Default for text-embedding-3-small
	if model == "text-embedding-3-large" {
		dims = 3072
	}

	return &OpenAIProvider{
		apiKey: apiKey,
		model:  model,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		dimensions: dims,
	}
}

// Dimensions returns the embedding dimension size
func (p *OpenAIProvider) Dimensions() int {
	return p.dimensions
}

// GenerateEmbedding generates an embedding vector for the given text
func (p *OpenAIProvider) GenerateEmbedding(text string) ([]float32, error) {
	// Truncate if too long (OpenAI has 8191 token limit)
	if len(text) > 32000 { // ~8k tokens rough estimate
		text = text[:32000]
	}

	reqBody := map[string]interface{}{
		"input": text,
		"model": p.model,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", "https://api.openai.com/v1/embeddings", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if len(result.Data) == 0 {
		return nil, fmt.Errorf("no embedding returned")
	}

	return result.Data[0].Embedding, nil
}

// EmbeddingService manages article embedding generation
type EmbeddingService struct {
	provider Provider
}

// NewEmbeddingService creates a new embedding service
func NewEmbeddingService(provider Provider) *EmbeddingService {
	return &EmbeddingService{
		provider: provider,
	}
}

// ArticleInput represents the input data for generating an article embedding
type ArticleInput struct {
	Title       string
	Description string
	FullText    string
	URL         string
}

// GenerateArticleEmbedding generates an embedding from article content
// Combines title, description, and full text with appropriate weighting
func (s *EmbeddingService) GenerateArticleEmbedding(article ArticleInput) ([]float32, error) {
	// Construct combined text with weighted importance
	// Title is most important (repeated 3x), then description (2x), then content
	var parts []string

	if article.Title != "" {
		// Title gets extra weight by repeating
		parts = append(parts, article.Title, article.Title, article.Title)
	}

	if article.Description != "" {
		// Description gets moderate weight
		parts = append(parts, article.Description, article.Description)
	}

	if article.FullText != "" {
		// Full text included once
		// Truncate if very long to keep reasonable token count
		text := article.FullText
		if len(text) > 10000 {
			text = text[:10000] // Keep first ~2500 tokens
		}
		parts = append(parts, text)
	}

	if len(parts) == 0 {
		return nil, fmt.Errorf("no content to embed")
	}

	combinedText := strings.Join(parts, "\n\n")

	return s.provider.GenerateEmbedding(combinedText)
}

// CosineSimilarity calculates the cosine similarity between two embedding vectors
// Returns a value between -1 and 1, where 1 means identical, 0 means orthogonal, -1 means opposite
func CosineSimilarity(a, b []float32) float32 {
	if len(a) != len(b) {
		return 0
	}

	var dotProduct, normA, normB float32
	for i := range a {
		dotProduct += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dotProduct / (sqrt(normA) * sqrt(normB))
}

// sqrt is a simple float32 square root
func sqrt(x float32) float32 {
	if x <= 0 {
		return 0
	}
	// Newton's method for square root
	guess := x / 2
	for i := 0; i < 10; i++ {
		guess = (guess + x/guess) / 2
	}
	return guess
}
