package estransport

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"
	
	"github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/esapi"
)

// NewESClientWithFallback tries socket first, then direct connection
func NewESClientWithFallback(socketPath string, directURLs []string, directTransport http.RoundTripper) (*ProxiedESClient, error) {
	// First, try the socket connection
	if _, err := os.Stat(socketPath); err == nil {
		client, err := NewProxiedESClient(socketPath)
		if err == nil {
			// Test the connection
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			
			if err := client.Ping(ctx); err == nil {
				fmt.Printf("✅ Using proxy socket: %s\n", socketPath)
				return client, nil
			}
			fmt.Printf("⚠️  Proxy socket not responding, falling back to direct connection\n")
		}
	}

	// Fallback to direct connection
	fmt.Printf("🔄 Using direct ES connection: %v\n", directURLs)
	
	cfg := elasticsearch.Config{
		Addresses: directURLs,
	}
	
	if directTransport != nil {
		cfg.Transport = directTransport
	}

	esClient, err := elasticsearch.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create direct ES client: %v", err)
	}

	return &ProxiedESClient{
		Client:     esClient,
		socketPath: "(direct connection)",
	}, nil
}

// TestConnectivity tests both socket and direct connection
func TestConnectivity(socketPath string, directURLs []string) {
	fmt.Println("🔍 Testing Elasticsearch connectivity...")
	
	// Test socket connection
	if _, err := os.Stat(socketPath); err == nil {
		client, err := NewProxiedESClient(socketPath)
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			
			if err := client.Ping(ctx); err == nil {
				fmt.Printf("✅ Socket proxy working: %s\n", socketPath)
				
				// Get some info
				if info, err := client.GetClusterInfo(ctx); err == nil {
					if name, ok := info["cluster_name"]; ok {
						fmt.Printf("   Cluster: %v\n", name)
					}
					if version, ok := info["version"].(map[string]interface{}); ok {
						if num, ok := version["number"]; ok {
							fmt.Printf("   Version: %v\n", num)
						}
					}
				}
			} else {
				fmt.Printf("❌ Socket proxy not responding: %v\n", err)
			}
		} else {
			fmt.Printf("❌ Failed to create socket client: %v\n", err)
		}
	} else {
		fmt.Printf("❌ Socket not found: %s\n", socketPath)
	}
	
	// Test direct connection (if provided)
	if len(directURLs) > 0 {
		fmt.Printf("🔄 Testing direct connection: %v\n", directURLs)
		cfg := elasticsearch.Config{
			Addresses: directURLs,
		}
		
		if client, err := elasticsearch.NewClient(cfg); err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			
			req := esapi.PingRequest{}
			if res, err := req.Do(ctx, client); err == nil && !res.IsError() {
				fmt.Printf("✅ Direct connection working\n")
			} else {
				fmt.Printf("❌ Direct connection failed: %v\n", err)
			}
		} else {
			fmt.Printf("❌ Failed to create direct client: %v\n", err)
		}
	}
}
