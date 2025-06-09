package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/elastic/go-elasticsearch/v8"
)

// Command represents a shell command record from Elasticsearch
type Command struct {
	Command     string    `json:"command"`
	Timestamp   time.Time `json:"@timestamp"`
	ReturnCode  int       `json:"return_code"`
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

// getMaskedEnvVar returns the masked/hashed value for sensitive environment variables
// This mirrors the logic in the preexec hook
func getMaskedEnvVar(key string, value string) string {
	patterns := []string{"secret", "password", "key"}
	for _, pattern := range patterns {
		matched, err := regexp.MatchString("(?i)"+pattern, key)
		if err != nil {
			log.Printf("error in regex: %s", err)
			return value // Return original value on error
		}
		if matched {
			return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(value)))
		}
	}
	return value
}

// shouldSkipEnvVar checks if an environment variable should be skipped
// This mirrors the logic in the preexec hook
func shouldSkipEnvVar(key string, value string) bool {
	// Skip the same variables that the preexec hook skips
	patterns := []string{"^_+", "^PS1$", "^TERM$", "^PWD$", "TOTALRECALLROOT"}
	for _, pattern := range patterns {
		matched, err := regexp.MatchString("(?i)"+pattern, key)
		if err != nil {
			log.Printf("error in regex: %s", err)
			continue
		}
		if matched {
			return true
		}
	}
	return false
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
	debug := flag.Bool("debug", false, "Print debug information including the query")
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
	query := map[string]interface{}{
		"size": *numResults,
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": []map[string]interface{}{
					{
						"term": map[string]interface{}{
							"pwd.keyword": searchDir,
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
		"_source": []string{"command", "@timestamp", "return_code", "env", "pwd"},
	}

	// Add current environment variables to improve relevance
	environ := os.Environ()
	shouldClauses := query["query"].(map[string]interface{})["bool"].(map[string]interface{})["should"].([]map[string]interface{})

	envVarsUsed := 0
	for _, env := range environ {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key, value := parts[0], parts[1]

		// Skip environment variables that the preexec hook doesn't store
		if shouldSkipEnvVar(key, value) {
			if *debug {
				fmt.Printf("Skipping env var: %s (filtered by preexec hook)\n", key)
			}
			continue
		}

		// Get the masked value (in case it's a sensitive variable)
		maskedValue := getMaskedEnvVar(key, value)

		// Add environment variable as should clause
		shouldClauses = append(shouldClauses, map[string]interface{}{
			"match": map[string]interface{}{
				fmt.Sprintf("env.%s", key): maskedValue,
			},
		})
		envVarsUsed++

		if *debug {
			if maskedValue != value {
				fmt.Printf("Using masked env var: %s = %s (original was masked)\n", key, maskedValue)
			} else {
				fmt.Printf("Using env var: %s = %s\n", key, value)
			}
		}
	}

	// Update the should clauses in the query
	query["query"].(map[string]interface{})["bool"].(map[string]interface{})["should"] = shouldClauses

	if *debug {
		fmt.Printf("Using %d environment variables for relevance scoring\n", envVarsUsed)
	}

	// Convert query to JSON
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(query); err != nil {
		log.Fatalf("Error encoding query: %s", err)
	}

	// Debug: Print the query if requested
	if *debug {
		fmt.Println("\nElasticsearch Query:")
		var prettyQuery bytes.Buffer
		if err := json.Indent(&prettyQuery, buf.Bytes(), "", "  "); err == nil {
			fmt.Println(prettyQuery.String())
		}
		fmt.Println()
	}

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

	if res.IsError() {
		log.Fatalf("Elasticsearch returned error: %s", res.String())
	}

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
			cmdDir := searchDir
			cmdDirs[cmd] = cmdDir
		}
	}

	if len(uniqueCommands) == 0 {
		fmt.Println("No commands found for the specified directory.")
		if *debug {
			fmt.Println("Try running with -debug to see the query being executed.")
		}
		return
	}

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

	// Print results header
	fmt.Printf("%-10s | %-50s | %s\n", "SCORE", "COMMAND", "DIRECTORY")
	fmt.Println(strings.Repeat("-", 100))

	// Print results
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
