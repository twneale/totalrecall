package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"totalrecall/pkg/estransport"
)

// EnhancedShelper provides intelligent command suggestions using the proxy
type EnhancedShelper struct {
	client     *estransport.ProxiedESClient
	socketPath string
}

func NewEnhancedShelper(socketPath string) (*EnhancedShelper, error) {
	// Create ES client with fallback
	directURLs := []string{"https://localhost:9243"} // HAProxy mTLS endpoint as fallback
	client, err := estransport.NewESClientWithFallback(socketPath, directURLs, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create ES client: %v", err)
	}

	return &EnhancedShelper{
		client:     client,
		socketPath: socketPath,
	}, nil
}

// CommandSuggestion represents a suggested command with relevance info
type CommandSuggestion struct {
	Command     string
	Score       float64
	Frequency   int
	LastUsed    time.Time
	Directory   string
	Context     string // Why this command is suggested
}

// GetRelevantCommands finds commands relevant to the current context
func (s *EnhancedShelper) GetRelevantCommands(pwd string, envVars map[string]string, limit int) ([]CommandSuggestion, error) {
	// Build enhanced query with environment context
	query := s.buildContextualQuery(pwd, envVars, limit)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Execute search via proxy
	result, err := s.client.SearchCommands(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("search failed: %v", err)
	}

	// Process and rank results
	suggestions := s.processSearchResults(result, pwd, envVars)

	return suggestions, nil
}

func (s *EnhancedShelper) buildContextualQuery(pwd string, envVars map[string]string, limit int) map[string]interface{} {
	// Build should clauses for environment variables
	shouldClauses := make([]map[string]interface{}, 0)
	
	for key, value := range envVars {
		// Skip noisy environment variables
		if s.shouldSkipEnvVar(key) {
			continue
		}
		
		// Boost important environment variables
		boost := s.getEnvVarBoost(key)
		
		shouldClauses = append(shouldClauses, map[string]interface{}{
			"match": map[string]interface{}{
				fmt.Sprintf("env.%s", key): map[string]interface{}{
					"query": value,
					"boost": boost,
				},
			},
		})
	}

	query := map[string]interface{}{
		"size": limit * 2, // Get more results for deduplication
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": []map[string]interface{}{
					{
						"term": map[string]interface{}{
							"pwd.keyword": pwd,
						},
					},
					{
						"term": map[string]interface{}{
							"return_code": 0, // Only successful commands
						},
					},
				},
				"should": shouldClauses,
			},
		},
		"sort": []map[string]interface{}{
			{"_score": map[string]interface{}{"order": "desc"}},
			{"start_timestamp": map[string]interface{}{"order": "desc"}},
		},
	}

	return query
}

func (s *EnhancedShelper) shouldSkipEnvVar(key string) bool {
	skipPatterns := []string{"HIST", "LESS", "LS_COLORS", "TERM", "PWD", "_"}
	for _, pattern := range skipPatterns {
		if strings.Contains(key, pattern) {
			return true
		}
	}
	return false
}

func (s *EnhancedShelper) getEnvVarBoost(key string) float64 {
	// Give higher boosts to more important environment variables
	highValue := []string{"NODE_ENV", "RAILS_ENV", "ENVIRONMENT", "AWS_PROFILE", "KUBERNETES_NAMESPACE"}
	mediumValue := []string{"USER", "HOME", "SHELL"}
	
	for _, important := range highValue {
		if key == important {
			return 3.0
		}
	}
	
	for _, medium := range mediumValue {
		if key == medium {
			return 2.0
		}
	}
	
	// Default boost for environment variables
	return 1.5
}

func (s *EnhancedShelper) processSearchResults(result *estransport.SearchResponse, pwd string, envVars map[string]string) []CommandSuggestion {
	// Track command frequency and recency
	commandStats := make(map[string]*CommandSuggestion)
	
	for _, hit := range result.Hits.Hits {
		cmd := hit.Source.Command
		
		if existing, exists := commandStats[cmd]; exists {
			existing.Frequency++
			if hit.Source.StartTimestamp.After(existing.LastUsed) {
				existing.LastUsed = hit.Source.StartTimestamp
				existing.Score = hit.Score // Update with latest score
			}
		} else {
			context := s.explainRelevance(hit.Source, envVars)
			
			commandStats[cmd] = &CommandSuggestion{
				Command:   cmd,
				Score:     hit.Score,
				Frequency: 1,
				LastUsed:  hit.Source.StartTimestamp,
				Directory: pwd,
				Context:   context,
			}
		}
	}
	
	// Convert to slice and sort by combined relevance
	suggestions := make([]CommandSuggestion, 0, len(commandStats))
	for _, suggestion := range commandStats {
		// Calculate combined score (ES score + frequency + recency)
		recencyScore := s.calculateRecencyScore(suggestion.LastUsed)
		frequencyScore := float64(suggestion.Frequency) * 0.1
		
		suggestion.Score = suggestion.Score + frequencyScore + recencyScore
		suggestions = append(suggestions, *suggestion)
	}
	
	// Sort by combined score
	sort.Slice(suggestions, func(i, j int) bool {
		return suggestions[i].Score > suggestions[j].Score
	})
	
	return suggestions
}

func (s *EnhancedShelper) calculateRecencyScore(lastUsed time.Time) float64 {
	daysSince := time.Since(lastUsed).Hours() / 24
	
	if daysSince < 1 {
		return 2.0 // Used today
	} else if daysSince < 7 {
		return 1.0 // Used this week
	} else if daysSince < 30 {
		return 0.5 // Used this month
	}
	
	return 0.0 // Older
}

func (s *EnhancedShelper) explainRelevance(cmd estransport.Command, currentEnv map[string]string) string {
	reasons := []string{}
	
	// Check environment matches
	matches := 0
	for key, currentValue := range currentEnv {
		if cmdValue, exists := cmd.Env[key]; exists && cmdValue == currentValue {
			matches++
		}
	}
	
	if matches > 0 {
		reasons = append(reasons, fmt.Sprintf("%d env vars match", matches))
	}
	
	// Check for patterns
	if strings.Contains(cmd.Command, "git") {
		reasons = append(reasons, "git workflow")
	}
	
	if strings.Contains(cmd.Command, "docker") {
		reasons = append(reasons, "docker operations")
	}
	
	if strings.Contains(cmd.Command, "kubectl") || strings.Contains(cmd.Command, "k8s") {
		reasons = append(reasons, "kubernetes")
	}
	
	if len(reasons) == 0 {
		reasons = append(reasons, "similar directory")
	}
	
	return strings.Join(reasons, ", ")
}

// GetCommandHistory retrieves recent command history for analysis
func (s *EnhancedShelper) GetCommandHistory(pwd string, hours int, limit int) ([]estransport.Command, error) {
	query := map[string]interface{}{
		"size": limit,
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": []map[string]interface{}{
					{
						"term": map[string]interface{}{
							"pwd.keyword": pwd,
						},
					},
					{
						"range": map[string]interface{}{
							"start_timestamp": map[string]interface{}{
								"gte": fmt.Sprintf("now-%dh", hours),
							},
						},
					},
				},
			},
		},
		"sort": []map[string]interface{}{
			{"start_timestamp": map[string]interface{}{"order": "desc"}},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := s.client.SearchCommands(ctx, query)
	if err != nil {
		return nil, err
	}

	commands := make([]estransport.Command, 0, len(result.Hits.Hits))
	for _, hit := range result.Hits.Hits {
		commands = append(commands, hit.Source)
	}

	return commands, nil
}

// Display functions
func (s *EnhancedShelper) DisplaySuggestions(suggestions []CommandSuggestion, maxResults int) {
	if len(suggestions) == 0 {
		fmt.Println("No relevant commands found for the current context.")
		return
	}

	fmt.Printf("📊 Found %d relevant commands:\n\n", len(suggestions))

	count := 0
	for _, suggestion := range suggestions {
		if count >= maxResults {
			break
		}

		// Format command for display
		displayCmd := suggestion.Command
		if len(displayCmd) > 60 {
			displayCmd = displayCmd[:57] + "..."
		}

		// Format frequency and recency
		lastUsedStr := formatTimeAgo(suggestion.LastUsed)
		
		fmt.Printf("%2d. %-60s (score: %.1f, used %dx, %s)\n", 
			count+1, displayCmd, suggestion.Score, suggestion.Frequency, lastUsedStr)
		
		if suggestion.Context != "" {
			fmt.Printf("    💡 %s\n", suggestion.Context)
		}
		
		fmt.Println()
		count++
	}

	if len(suggestions) > maxResults {
		fmt.Printf("... and %d more suggestions\n", len(suggestions)-maxResults)
	}
}

func formatTimeAgo(t time.Time) string {
	duration := time.Since(t)
	
	if duration.Hours() < 1 {
		return fmt.Sprintf("%.0f min ago", duration.Minutes())
	} else if duration.Hours() < 24 {
		return fmt.Sprintf("%.0f hours ago", duration.Hours())
	} else {
		days := int(duration.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	}
}

// Helper function to get current environment
func getCurrentEnv() map[string]string {
	env := make(map[string]string)
	for _, e := range os.Environ() {
		pair := strings.SplitN(e, "=", 2)
		if len(pair) == 2 {
			env[pair[0]] = pair[1]
		}
	}
	return env
}

// Main function
func main() {
	var (
		socketPath   = flag.String("socket", "/tmp/totalrecall-proxy.sock", "Unix domain socket path")
		numResults   = flag.Int("n", 10, "Number of results to return")
		pwd          = flag.String("pwd", "", "Working directory (default: current directory)")
		showHistory  = flag.Bool("history", false, "Show recent command history instead of suggestions")
		historyHours = flag.Int("hours", 24, "Hours of history to show")
		testConn     = flag.Bool("test", false, "Test connectivity to ES via proxy")
		debug        = flag.Bool("debug", false, "Enable debug output")
	)
	flag.Parse()

	if *debug {
		// Enable debug logging if needed
	}

	// Get current directory if not specified
	if *pwd == "" {
		var err error
		*pwd, err = os.Getwd()
		if err != nil {
			log.Fatalf("Error getting current directory: %v", err)
		}
	}

	// Create enhanced shelper
	shelper, err := NewEnhancedShelper(*socketPath)
	if err != nil {
		log.Fatalf("Error creating shelper: %v", err)
	}

	fmt.Printf("🔍 Total Recall Enhanced Shelper\n")
	fmt.Printf("Using: %s\n", shelper.socketPath)
	fmt.Printf("Directory: %s\n\n", *pwd)

	if *testConn {
		directURLs := []string{"https://localhost:9243"}
		estransport.TestConnectivity(*socketPath, directURLs)
		return
	}

	if *showHistory {
		history, err := shelper.GetCommandHistory(*pwd, *historyHours, *numResults)
		if err != nil {
			log.Fatalf("Error getting history: %v", err)
		}
		
		fmt.Printf("📜 Recent command history (%d hours):\n\n", *historyHours)
		for i, cmd := range history {
			duration := cmd.EndTimestamp.Sub(cmd.StartTimestamp)
			statusIcon := "✅"
			if cmd.ReturnCode != 0 {
				statusIcon = "❌"
			}
			
			fmt.Printf("%2d. %s %s (%s, %dms)\n", 
				i+1, statusIcon, cmd.Command, 
				cmd.StartTimestamp.Format("15:04:05"), 
				duration.Milliseconds())
		}
		return
	}

	// Get current environment
	currentEnv := getCurrentEnv()
	
	if *debug {
		fmt.Printf("🔧 Using %d environment variables for context\n", len(currentEnv))
	}

	// Get command suggestions
	suggestions, err := shelper.GetRelevantCommands(*pwd, currentEnv, *numResults)
	if err != nil {
		log.Fatalf("Error getting suggestions: %v", err)
	}

	// Display results
	shelper.DisplaySuggestions(suggestions, *numResults)
}
