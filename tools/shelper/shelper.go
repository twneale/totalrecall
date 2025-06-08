package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/elastic/go-elasticsearch/v8"
)

// Command represents a shell command record from Elasticsearch
type Command struct {
	Command     string    `json:"command"`
	Timestamp   time.Time `json:"@timestamp"`
	ReturnCode  int       `json:"return_code"`
	Environment struct {
		OLDPWD string `json:"OLDPWD"`
	} `json:"env"`
}

// SearchResult represents the Elasticsearch search results
type SearchResult struct {
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

func main() {
	// Parse command line flags
	esHost := flag.String("host", "http://localhost:9200", "Elasticsearch host URL")
	esIndex := flag.String("index", "totalrecall", "Elasticsearch index name")
	numResults := flag.Int("n", 10, "Number of results to return")
	usePwd := flag.Bool("pwd", true, "Use current PWD (true) or specify a directory")
	dirPath := flag.String("dir", "", "Directory path to search for (if -pwd=false)")
	username := flag.String("user", "", "Elasticsearch username")
	password := flag.String("pass", "", "Elasticsearch password")
	flag.Parse()

	// Determine which directory to search for
	searchDir := ""
	if *usePwd {
		var err error
		searchDir, err = os.Getwd()
		if err != nil {
			log.Fatalf("Error getting current directory: %s", err)
		}
	} else {
		searchDir = *dirPath
		if searchDir == "" {
			log.Fatal("Please provide a directory with -dir if -pwd=false")
		}
	}

	fmt.Printf("Searching for commands executed in: %s\n", searchDir)

	// Create Elasticsearch config
	cfg := elasticsearch.Config{
		Addresses: []string{*esHost},
	}

	// Add basic auth if provided
	if *username != "" && *password != "" {
		cfg.Username = *username
		cfg.Password = *password
	}

	// Create Elasticsearch client
	es, err := elasticsearch.NewClient(cfg)
	if err != nil {
		log.Fatalf("Error creating Elasticsearch client: %s", err)
	}

	// Check if Elasticsearch is available
	res, err := es.Info()
	if err != nil {
		log.Fatalf("Error connecting to Elasticsearch: %s", err)
	}
	res.Body.Close()

	// Build the search query with enhanced relevance based on environment
	// Initialize query structure
	query := map[string]interface{}{
		"size": *numResults,
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": []map[string]interface{}{
					{
						"term": map[string]interface{}{
							"env.OLDPWD.keyword": searchDir,
						},
					},
					{
						"match": map[string]interface{}{
							"return_code": 0, // Only include successful commands
						},
					},
				},
				"should": []map[string]interface{}{},
			},
		},
		"sort": []map[string]interface{}{
			{
				"_score": map[string]interface{}{
					"order": "desc",
				},
			},
			{
				"@timestamp": map[string]interface{}{
					"order": "desc",
				},
			},
		},
		"_source": []string{"command", "@timestamp", "return_code", "env"},
	}
	
	// Add current environment variables to improve relevance
	environ := os.Environ()
	shouldClauses := query["query"].(map[string]interface{})["bool"].(map[string]interface{})["should"].([]map[string]interface{})
	
	for _, env := range environ {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) != 2 {
			continue
		}
		
		key, value := parts[0], parts[1]
		
		// Skip OLDPWD as it's already in the must clause
		if key == "OLDPWD" {
			continue
		}
        // Skip PWD to prevent getting wrong commands.
        if key == "PWD" {
            continue
        }
		
		// Add all other environment variables as should clauses
		shouldClauses = append(shouldClauses, map[string]interface{}{
			"match": map[string]interface{}{
				fmt.Sprintf("env.%s", key): value,
			},
		})
	}
	
	// Update the should clauses in the query
	query["query"].(map[string]interface{})["bool"].(map[string]interface{})["should"] = shouldClauses

	// Convert query to JSON
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(query); err != nil {
		log.Fatalf("Error encoding query: %s", err)
	}

	// Debug: Print the query (optional, comment out in production)
	fmt.Println("Query:")
	json.NewEncoder(os.Stdout).Encode(query)
	
	// Execute the search
	res, err = es.Search(
		es.Search.WithContext(context.Background()),
		es.Search.WithIndex(*esIndex),
		es.Search.WithBody(&buf),
		es.Search.WithTrackTotalHits(true),
	)
	if err != nil {
		log.Fatalf("Error searching Elasticsearch: %s", err)
	}
	defer res.Body.Close()

	// Parse the response
	var result SearchResult
	if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
		log.Fatalf("Error parsing response: %s", err)
	}

	// Display the results
	fmt.Printf("Found %d commands\n\n", result.Hits.Total.Value)
	
	// Extract unique commands (to avoid duplicates)
	uniqueCommands := make(map[string]float64)
	cmdDirs := make(map[string]string) // Store directory for each command
	
	for _, hit := range result.Hits.Hits {
		cmd := hit.Source.Command
		// Skip empty commands
		if cmd == "" {
			continue
		}
		// Keep only the highest score for each unique command
		if _, exists := uniqueCommands[cmd]; !exists || hit.Score > uniqueCommands[cmd] {
			uniqueCommands[cmd] = hit.Score
			cmdDirs[cmd] = hit.Source.Environment.OLDPWD
		}
	}

	// Sort and print commands
	fmt.Printf("%-10s | %s\n", "SCORE", "COMMAND")
	fmt.Println(strings.Repeat("-", 80))

	// Convert map to sorted slice (by score)
	type cmdScore struct {
		Command string
		Score   float64
	}
	sortedCommands := make([]cmdScore, 0, len(uniqueCommands))
	for cmd, score := range uniqueCommands {
		sortedCommands = append(sortedCommands, cmdScore{cmd, score})
	}

	// Sort by score (highest first)
	for i := 0; i < len(sortedCommands); i++ {
		max := i
		for j := i + 1; j < len(sortedCommands); j++ {
			if sortedCommands[j].Score > sortedCommands[max].Score {
				max = j
			}
		}
		sortedCommands[i], sortedCommands[max] = sortedCommands[max], sortedCommands[i]
	}

	// Print results
	fmt.Printf("%-10s | %-50s | %s\n", "SCORE", "COMMAND", "DIRECTORY")
	fmt.Println(strings.Repeat("-", 100))
	
	count := 0
	for _, cmd := range sortedCommands {
		// Get directory (basename only for cleaner display)
		dirPath := cmdDirs[cmd.Command]
		dirName := filepath.Base(dirPath)
		
		// If current directory, add a marker
		if dirPath == searchDir {
			dirName = "*" + dirName
		}
		
		fmt.Printf("%-10.2f | %-50s | %s\n", cmd.Score, cmd.Command, dirName)
		count++
		if count >= *numResults {
			break
		}
	}
}
