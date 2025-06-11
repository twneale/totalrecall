package estransport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"
	
	"github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/esapi"
)

// ProxiedESClient wraps the standard ES client with socket transport
type ProxiedESClient struct {
	*elasticsearch.Client
	socketPath string
	transport  *UnixSocketTransport
}

func NewProxiedESClient(socketPath string) (*ProxiedESClient, error) {
	transport := NewUnixSocketTransport(socketPath)
	
	cfg := elasticsearch.Config{
		Transport: transport,
		// These addresses are required by the client but ignored due to custom transport
		Addresses: []string{"http://elasticsearch:9200"}, // Placeholder
	}

	client, err := elasticsearch.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create ES client: %v", err)
	}

	return &ProxiedESClient{
		Client:     client,
		socketPath: socketPath,
		transport:  transport,
	}, nil
}

// Convenience method to test connectivity
func (c *ProxiedESClient) Ping(ctx context.Context) error {
	req := esapi.PingRequest{}
	res, err := req.Do(ctx, c.Client)
	if err != nil {
		return fmt.Errorf("ping failed: %v", err)
	}
	defer res.Body.Close()
	
	if res.IsError() {
		return fmt.Errorf("ping returned error: %s", res.Status())
	}
	
	return nil
}

// Enhanced search method with better error handling
func (c *ProxiedESClient) SearchCommands(ctx context.Context, query map[string]interface{}) (*SearchResponse, error) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(query); err != nil {
		return nil, fmt.Errorf("failed to encode query: %v", err)
	}

	req := esapi.SearchRequest{
		Index: []string{"totalrecall*"},
		Body:  &buf,
	}

	res, err := req.Do(ctx, c.Client)
	if err != nil {
		return nil, fmt.Errorf("search request failed: %v", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		var errorResp map[string]interface{}
		if err := json.NewDecoder(res.Body).Decode(&errorResp); err != nil {
			return nil, fmt.Errorf("ES error %s (failed to decode error response)", res.Status())
		}
		return nil, fmt.Errorf("ES error %s: %v", res.Status(), errorResp)
	}

	var searchResp SearchResponse
	if err := json.NewDecoder(res.Body).Decode(&searchResp); err != nil {
		return nil, fmt.Errorf("failed to decode search response: %v", err)
	}

	return &searchResp, nil
}

// Get cluster info
func (c *ProxiedESClient) GetClusterInfo(ctx context.Context) (map[string]interface{}, error) {
	req := esapi.InfoRequest{}
	res, err := req.Do(ctx, c.Client)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.IsError() {
		return nil, fmt.Errorf("cluster info error: %s", res.Status())
	}

	var info map[string]interface{}
	if err := json.NewDecoder(res.Body).Decode(&info); err != nil {
		return nil, err
	}

	return info, nil
}

// Get index stats
func (c *ProxiedESClient) GetIndexStats(ctx context.Context, indices ...string) (map[string]interface{}, error) {
	req := esapi.IndicesStatsRequest{
		Index: indices,
	}
	
	res, err := req.Do(ctx, c.Client)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.IsError() {
		return nil, fmt.Errorf("index stats error: %s", res.Status())
	}

	var stats map[string]interface{}
	if err := json.NewDecoder(res.Body).Decode(&stats); err != nil {
		return nil, err
	}

	return stats, nil
}

// Response structures
type SearchResponse struct {
	Took     int  `json:"took"`
	TimedOut bool `json:"timed_out"`
	Hits     struct {
		Total struct {
			Value    int    `json:"value"`
			Relation string `json:"relation"`
		} `json:"total"`
		MaxScore float64 `json:"max_score"`
		Hits     []struct {
			Index  string  `json:"_index"`
			ID     string  `json:"_id"`
			Score  float64 `json:"_score"`
			Source Command `json:"_source"`
		} `json:"hits"`
	} `json:"hits"`
}

type Command struct {
	Command        string            `json:"command"`
	Timestamp      time.Time         `json:"@timestamp"`
	StartTimestamp time.Time         `json:"start_timestamp"`
	EndTimestamp   time.Time         `json:"end_timestamp"`
	ReturnCode     int               `json:"return_code"`
	Pwd            string            `json:"pwd"`
	Hostname       string            `json:"hostname"`
	IPAddress      string            `json:"ip_address,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
}
