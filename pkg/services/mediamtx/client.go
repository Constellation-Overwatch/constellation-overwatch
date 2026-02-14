package mediamtx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/logger"
)

// PathInfo represents a single stream path from MediaMTX API
type PathInfo struct {
	Name         string    `json:"name"`
	Ready        bool      `json:"ready"`
	ReadyTime    time.Time `json:"readyTime,omitempty"`
	Tracks       []string  `json:"tracks,omitempty"`
	BytesReceived int64    `json:"bytesReceived,omitempty"`
	Readers      []Reader  `json:"readers,omitempty"`
}

// Reader represents a connected reader/viewer
type Reader struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// PathListResponse is the response from MediaMTX /v3/paths/list
type PathListResponse struct {
	PageCount int        `json:"pageCount"`
	ItemCount int        `json:"itemCount"`
	Items     []PathInfo `json:"items"`
}

// StreamStatus represents the processed status of a video stream
type StreamStatus struct {
	Path        string    // Full path (e.g., "station-1/drone-1")
	StationID   string    // Station/Org ID
	EntityID    string    // Entity ID
	IsOnline    bool      // Whether stream is active
	Readers     int       // Number of active viewers
	LastChecked time.Time // When this status was last verified
}

// Client provides access to MediaMTX API
type Client struct {
	apiURL     string
	httpClient *http.Client

	// Cache of stream statuses
	cacheMu     sync.RWMutex
	streamCache map[string]*StreamStatus
	lastPoll    time.Time
	pollTTL     time.Duration
}

// NewClient creates a new MediaMTX API client
func NewClient(apiURL string) *Client {
	if apiURL == "" {
		apiURL = "http://localhost:9997"
	}

	return &Client{
		apiURL: strings.TrimSuffix(apiURL, "/"),
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
		streamCache: make(map[string]*StreamStatus),
		pollTTL:     2 * time.Second, // Cache TTL
	}
}

// GetActivePaths fetches all active paths from MediaMTX
func (c *Client) GetActivePaths(ctx context.Context) ([]PathInfo, error) {
	url := c.apiURL + "/v3/paths/list"

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch paths: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("MediaMTX API returned status %d", resp.StatusCode)
	}

	var pathList PathListResponse
	if err := json.NewDecoder(resp.Body).Decode(&pathList); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return pathList.Items, nil
}

// GetStreamStatuses returns the current status of all streams
// Uses caching to avoid hammering the API
func (c *Client) GetStreamStatuses(ctx context.Context) (map[string]*StreamStatus, error) {
	c.cacheMu.RLock()
	if time.Since(c.lastPoll) < c.pollTTL && len(c.streamCache) > 0 {
		// Return cached data
		result := make(map[string]*StreamStatus, len(c.streamCache))
		for k, v := range c.streamCache {
			result[k] = v
		}
		c.cacheMu.RUnlock()
		return result, nil
	}
	c.cacheMu.RUnlock()

	// Fetch fresh data
	paths, err := c.GetActivePaths(ctx)
	if err != nil {
		return nil, err
	}

	// Update cache
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()

	// Clear old entries
	c.streamCache = make(map[string]*StreamStatus)

	now := time.Now()
	for _, path := range paths {
		status := c.parsePathToStatus(path, now)
		if status != nil {
			c.streamCache[status.EntityID] = status
		}
	}

	c.lastPoll = now
	logger.Debugw("MediaMTX paths refreshed", "count", len(c.streamCache))

	// Return copy
	result := make(map[string]*StreamStatus, len(c.streamCache))
	for k, v := range c.streamCache {
		result[k] = v
	}
	return result, nil
}

// IsStreamOnline checks if a specific entity's stream is online
func (c *Client) IsStreamOnline(ctx context.Context, entityID string) bool {
	statuses, err := c.GetStreamStatuses(ctx)
	if err != nil {
		logger.Debugw("Failed to get stream statuses", "error", err)
		return false
	}

	status, exists := statuses[entityID]
	return exists && status.IsOnline
}

// GetStreamStatus returns the status for a specific entity
func (c *Client) GetStreamStatus(ctx context.Context, entityID string) *StreamStatus {
	statuses, err := c.GetStreamStatuses(ctx)
	if err != nil {
		return nil
	}
	return statuses[entityID]
}

// parsePathToStatus converts a PathInfo to StreamStatus
// Handles path formats: "{entity_id}" or "{station_id}/{entity_id}"
func (c *Client) parsePathToStatus(path PathInfo, now time.Time) *StreamStatus {
	if path.Name == "" {
		return nil
	}

	status := &StreamStatus{
		Path:        path.Name,
		IsOnline:    path.Ready,
		Readers:     len(path.Readers),
		LastChecked: now,
	}

	// Parse path: could be "entity_id" or "station_id/entity_id"
	parts := strings.Split(path.Name, "/")
	switch len(parts) {
	case 1:
		status.EntityID = parts[0]
		status.StationID = ""
	case 2:
		status.StationID = parts[0]
		status.EntityID = parts[1]
	default:
		// Handle deeper paths: station/sub/entity -> station/sub, entity
		status.EntityID = parts[len(parts)-1]
		status.StationID = strings.Join(parts[:len(parts)-1], "/")
	}

	return status
}

// StartPolling begins background polling of MediaMTX API
// Returns a channel that receives updates when stream status changes
func (c *Client) StartPolling(ctx context.Context, interval time.Duration) <-chan map[string]*StreamStatus {
	updates := make(chan map[string]*StreamStatus, 1)

	go func() {
		defer close(updates)

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		// Initial poll
		if statuses, err := c.GetStreamStatuses(ctx); err == nil {
			select {
			case updates <- statuses:
			default:
			}
		}

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				statuses, err := c.GetStreamStatuses(ctx)
				if err != nil {
					logger.Warnw("MediaMTX poll failed", "error", err)
					continue
				}

				// Send update (non-blocking)
				select {
				case updates <- statuses:
				default:
					// Skip if receiver is busy
				}
			}
		}
	}()

	return updates
}

// GetAPIURL returns the configured API URL
func (c *Client) GetAPIURL() string {
	return c.apiURL
}
