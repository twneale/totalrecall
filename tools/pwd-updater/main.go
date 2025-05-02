package main

import (
	"encoding/json"
	"log"
	"os"
	"time"

	"github.com/nats-io/nats.go"
)

// CommandData represents the shell command and environment
type CommandData struct {
	Environment map[string]string `json:"environment"`
	Timestamp   time.Time         `json:"timestamp"`
}

func main() {

	// NATS connection setup
	natsURL := os.Getenv("NATS_URL")
	if natsURL == "" {
		natsURL = nats.DefaultURL // localhost:4222
	}

	// TLS configuration
	opts := []nats.Option{}
	
	// Add TLS options if enabled
	if os.Getenv("NATS_TLS") == "true" {
		// If you have client certificates
		if os.Getenv("NATS_CERT") != "" && os.Getenv("NATS_KEY") != "" {
			opts = append(opts, nats.ClientCert(
				os.Getenv("NATS_CERT"),
				os.Getenv("NATS_KEY"),
			))
		}
		
		// If you have a CA certificate
		if os.Getenv("NATS_CA") != "" {
			opts = append(opts, nats.RootCAs(os.Getenv("NATS_CA")))
		}
	}

	// Connect to NATS with timeout
	nc, err := nats.Connect(natsURL, opts...)
	if err != nil {
		log.Fatalf("Error connecting to NATS: %v", err)
	}
	defer nc.Close()

	// Create the data structure
	data := CommandData{
		Environment: map[string]string{
			"PWD": os.Getenv("PWD"),
		},
		Timestamp: time.Now(),
	}

	// Convert to JSON
	jsonData, err := json.Marshal(data)
	if err != nil {
		log.Fatalf("Error marshaling JSON: %v", err)
	}

	// Publish to NATS
	subject := "totalrecall"
	err = nc.Publish(subject, jsonData)
	if err != nil {
		log.Fatalf("Error publishing to NATS: %v", err)
	}

	// Ensure delivery with flush
	err = nc.Flush()
	if err != nil {
		log.Fatalf("Error flushing NATS connection: %v", err)
	}
}
