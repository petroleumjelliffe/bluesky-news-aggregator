package jetstream

import (
	"context"
	"fmt"
	"log"
	"log/slog"

	jsclient "github.com/bluesky-social/jetstream/pkg/client"
	"github.com/bluesky-social/jetstream/pkg/client/schedulers/sequential"
	"github.com/bluesky-social/jetstream/pkg/models"
)

// EventHandler is called for each event received from Jetstream
type EventHandler func(ctx context.Context, event *models.Event) error

// Client wraps the Jetstream client
type Client struct {
	client  *jsclient.Client
	handler EventHandler
	logger  *slog.Logger
}

// Config holds Jetstream client configuration
type Config struct {
	WebsocketURL      string
	Compress          bool
	WantedCollections []string
	WantedDIDs        []string
}

// NewClient creates a new Jetstream client
func NewClient(cfg *Config, handler EventHandler) (*Client, error) {
	logger := slog.Default()

	// Create sequential scheduler that calls our handler
	scheduler := sequential.NewScheduler(
		"firehose-consumer",
		logger,
		func(ctx context.Context, event *models.Event) error {
			// Call handler
			if err := handler(ctx, event); err != nil {
				log.Printf("[ERROR] Handler failed for event: %v", err)
				return err
			}
			return nil
		},
	)

	// Create Jetstream client config
	clientCfg := &jsclient.ClientConfig{
		WebsocketURL:      cfg.WebsocketURL,
		Compress:          cfg.Compress,
		WantedCollections: cfg.WantedCollections,
		WantedDids:        cfg.WantedDIDs,
		ExtraHeaders:      make(map[string]string), // Initialize to avoid nil map panic
	}

	// Create client
	client, err := jsclient.NewClient(
		clientCfg,
		logger,
		scheduler,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}

	return &Client{
		client:  client,
		handler: handler,
		logger:  logger,
	}, nil
}

// Connect establishes WebSocket connection and starts reading events
func (c *Client) Connect(ctx context.Context, cursor *int64) error {
	log.Printf("[INFO] Connecting to Jetstream...")
	if cursor != nil {
		log.Printf("[INFO] Resuming from cursor: %d", *cursor)
	}

	if err := c.client.ConnectAndRead(ctx, cursor); err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}

	return nil
}

// Stats returns connection statistics
func (c *Client) Stats() (bytesRead, eventsRead int64) {
	return c.client.BytesRead.Load(), c.client.EventsRead.Load()
}
