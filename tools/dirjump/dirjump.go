package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/elastic/go-elasticsearch/v8"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// ESHit represents a single hit from Elasticsearch
type ESHit struct {
	Source struct {
		Command       string    `json:"command"`
		StartTimestamp time.Time `json:"start_timestamp"`
		Env           struct {
			PWD string `json:"PWD"`
		} `json:"env"`
	} `json:"_source"`
}

// ESResponse represents the Elasticsearch search response
type ESResponse struct {
	Hits struct {
		Hits []ESHit `json:"hits"`
	} `json:"hits"`
}

// DirScore represents a directory with its score
type DirScore struct {
	Path  string
	Score float64
}

func main() {
	// Connect to Elasticsearch
	cfg := elasticsearch.Config{
		Addresses: []string{"http://localhost:9200"},
	}
	
	// Check if ES_URL environment variable is set
	if esURL := os.Getenv("ES_URL"); esURL != "" {
		cfg.Addresses = []string{esURL}
	}
	
	es, err := elasticsearch.NewClient(cfg)
	if err != nil {
		log.Fatalf("Error creating the client: %s", err)
	}

	// Query Elasticsearch for recent commands
	dirScores := getDirScores(es)
	
	// Create and run the terminal UI
	app := tview.NewApplication()
	list := tview.NewList().
		SetHighlightFullLine(true).
		SetWrapAround(true)

	// Add directory options to the list
	for i, dir := range dirScores {
		if i >= 10 { // Limit to top 10 directories
			break
		}
		
		// Get directory name for display
		dirName := dir.Path
		if lastSlash := strings.LastIndex(dirName, "/"); lastSlash >= 0 {
			shortName := dirName[lastSlash+1:]
			if shortName == "" {
				shortName = "/"
			}
			dirName = fmt.Sprintf("%s (%s)", shortName, dirName)
		}
		
		list.AddItem(dirName, dir.Path, rune('1'+i), nil)
	}

	// Set up key handling for selection
	list.SetSelectedFunc(func(i int, _ string, secondaryText string, _ rune) {
		app.Stop()
		
		// Output the selected directory to stdout
		fmt.Println(secondaryText)
		
		// Create a script file that will be sourced by bash
		tmpfile, err := ioutil.TempFile("", "dirjump*.sh")
		if err == nil {
			defer os.Remove(tmpfile.Name())
			tmpfile.WriteString(fmt.Sprintf("cd \"%s\"\n", secondaryText))
			tmpfile.Close()
			
			// Output the script path to stderr so the bash wrapper can source it
			fmt.Fprintf(os.Stderr, "%s", tmpfile.Name())
		}
	})

	// Set up key handler for immediate exit
	list.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEsc {
			app.Stop()
			return nil
		}
		return event
	})

	// Set up the layout
	frame := tview.NewFrame(list).
		SetBorders(2, 2, 2, 2, 4, 4).
		AddText("Jump to Directory", true, tview.AlignCenter, tcell.ColorYellow).
		AddText("Use arrow keys to select, Enter to choose, Esc to cancel", false, tview.AlignCenter, tcell.ColorWhite)

	if err := app.SetRoot(frame, true).SetFocus(list).Run(); err != nil {
		log.Fatalf("Error running application: %s", err)
	}
}

// getDirScores queries Elasticsearch and returns directories with scores
func getDirScores(es *elasticsearch.Client) []DirScore {
	// Build the query to get cd commands and PWD changes
	var buf bytes.Buffer
	query := map[string]interface{}{
		"size": 500,
		"sort": []map[string]interface{}{
			{
				"start_timestamp": map[string]interface{}{
					"order": "desc",
				},
			},
		},
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"should": []map[string]interface{}{
					{
						"match_phrase_prefix": map[string]interface{}{
							"command": "cd ",
						},
					},
				},
			},
		},
	}
	
	if err := json.NewEncoder(&buf).Encode(query); err != nil {
		log.Fatalf("Error encoding query: %s", err)
	}

	// Perform the search request
	res, err := es.Search(
		es.Search.WithContext(context.Background()),
		es.Search.WithIndex("totalrecall"),  // Use the index from the example
		es.Search.WithBody(&buf),
		es.Search.WithTrackTotalHits(true),
	)
	if err != nil {
		log.Fatalf("Error getting response: %s", err)
	}
	defer res.Body.Close()

	// Parse the response
	var response ESResponse
	if err := json.NewDecoder(res.Body).Decode(&response); err != nil {
		log.Fatalf("Error parsing response: %s", err)
	}

	// Extract directories and calculate scores
	dirFreq := make(map[string]int)
	dirLastUsed := make(map[string]time.Time)
	
	// Also query current working directories from PWD
	var bufPwd bytes.Buffer
	queryPwd := map[string]interface{}{
		"size": 200,
		"sort": []map[string]interface{}{
			{
				"start_timestamp": map[string]interface{}{
					"order": "desc",
				},
			},
		},
		"_source": []string{"env.PWD", "start_timestamp"},
	}
	
	if err := json.NewEncoder(&bufPwd).Encode(queryPwd); err != nil {
		log.Fatalf("Error encoding PWD query: %s", err)
	}

	// Perform the PWD search request
	resPwd, err := es.Search(
		es.Search.WithContext(context.Background()),
		es.Search.WithIndex("totalrecall"),
		es.Search.WithBody(&bufPwd),
		es.Search.WithTrackTotalHits(true),
	)
	if err != nil {
		log.Fatalf("Error getting PWD response: %s", err)
	}
	defer resPwd.Body.Close()

	// Parse the PWD response
	var responsePwd ESResponse
	if err := json.NewDecoder(resPwd.Body).Decode(&responsePwd); err != nil {
		log.Fatalf("Error parsing PWD response: %s", err)
	}

	// Process CD commands
	for _, hit := range response.Hits.Hits {
		if strings.HasPrefix(hit.Source.Command, "cd ") {
			// Extract the directory from the cd command
			parts := strings.SplitN(hit.Source.Command, " ", 2)
			if len(parts) >= 2 {
				dir := strings.Trim(parts[1], "\"' ")
				
				// Resolve relative paths or ~
				if strings.HasPrefix(dir, "~") {
					home, err := os.UserHomeDir()
					if err == nil {
						dir = strings.Replace(dir, "~", home, 1)
					}
				} else if !strings.HasPrefix(dir, "/") {
					// For relative paths, we need the PWD at that time
					if hit.Source.Env.PWD != "" {
						dir = hit.Source.Env.PWD + "/" + dir
					}
				}
				
				// Only include directories that actually exist
				if _, err := os.Stat(dir); err == nil {
					dirFreq[dir]++
					if dirLastUsed[dir].Before(hit.Source.StartTimestamp) {
						dirLastUsed[dir] = hit.Source.StartTimestamp
					}
				}
			}
		}
	}

	// Process PWD records
	for _, hit := range responsePwd.Hits.Hits {
		if hit.Source.Env.PWD != "" {
			dir := hit.Source.Env.PWD
			if _, err := os.Stat(dir); err == nil {
				dirFreq[dir]++
				if dirLastUsed[dir].Before(hit.Source.StartTimestamp) {
					dirLastUsed[dir] = hit.Source.StartTimestamp
				}
			}
		}
	}

	// Add current directory and parent directories with some score
	if pwd, err := os.Getwd(); err == nil {
		// Add current directory
		dirFreq[pwd]++
		if dirLastUsed[pwd].IsZero() {
			dirLastUsed[pwd] = time.Now()
		}
		
		// Add parent directories with decreasing scores
		parts := strings.Split(pwd, "/")
		path := ""
		for i, part := range parts {
			if i == 0 && part == "" {
				path = "/"
			} else if part != "" {
				path = path + "/" + part
				if path != pwd { // Don't double-count current dir
					dirFreq[path] = dirFreq[path] + 1
					if dirLastUsed[path].IsZero() {
						dirLastUsed[path] = time.Now().Add(-time.Duration(len(parts)-i) * time.Hour)
					}
				}
			}
		}
	}

	// Calculate scores based on frequency and recency
	var dirScores []DirScore
	now := time.Now()
	
	for dir, freq := range dirFreq {
		lastUsed := dirLastUsed[dir]
		if lastUsed.IsZero() {
			lastUsed = now.Add(-24 * time.Hour)
		}
		
		// Score formula: combination of frequency and recency
		hoursAgo := now.Sub(lastUsed).Hours()
		recencyScore := 100.0 / (1.0 + hoursAgo/24.0) // Normalize to days
		
		// Combined score: 70% recency, 30% frequency
		score := 0.7*recencyScore + 0.3*float64(freq)
		
		dirScores = append(dirScores, DirScore{
			Path:  dir,
			Score: score,
		})
	}

	// Sort by score
	sort.Slice(dirScores, func(i, j int) bool {
		return dirScores[i].Score > dirScores[j].Score
	})

	return dirScores
}
