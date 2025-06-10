package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
    "io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// Event represents a shell command event
type Event struct {
	Command        string                 `json:"command"`
	ReturnCode     int                    `json:"return_code"`
	StartTimestamp time.Time              `json:"start_timestamp"`
	EndTimestamp   time.Time              `json:"end_timestamp"`
	Pwd            string                 `json:"pwd"`
	Hostname       string                 `json:"hostname"`
	IPAddress      string                 `json:"ip_address,omitempty"`
	Env            map[string]string      `json:"env,omitempty"`
	ConfigVersion  string                 `json:"_config_version,omitempty"`
}

// PubSubClient handles connection to the proxy
type PubSubClient struct {
	socketPath string
	conn       net.Conn
	scanner    *bufio.Scanner
}

func NewPubSubClient(socketPath string) *PubSubClient {
	return &PubSubClient{
		socketPath: socketPath,
	}
}

func (c *PubSubClient) Connect() error {
	conn, err := net.Dial("unix", c.socketPath)
	if err != nil {
		return fmt.Errorf("failed to connect to %s: %v", c.socketPath, err)
	}
	
	c.conn = conn
	c.scanner = bufio.NewScanner(conn)
	return nil
}

func (c *PubSubClient) Subscribe(subscriberID string, filter string) error {
	if c.conn == nil {
		return fmt.Errorf("not connected")
	}
	
	subscribeCmd := fmt.Sprintf("SUBSCRIBE %s", subscriberID)
	if filter != "" {
		subscribeCmd += " " + filter
	}
	subscribeCmd += "\n"
	
	_, err := c.conn.Write([]byte(subscribeCmd))
	return err
}

func (c *PubSubClient) ReadEvent() (*Event, error) {
	if !c.scanner.Scan() {
		if err := c.scanner.Err(); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("connection closed")
	}
	
	line := strings.TrimSpace(c.scanner.Text())
	
	// Skip empty lines
	if line == "" {
		return c.ReadEvent() // Recursively read next event
	}
	
	// Skip protocol messages
	if strings.HasPrefix(line, "SUBSCRIBED") || strings.HasPrefix(line, "PONG") {
		return c.ReadEvent() // Recursively read next event
	}
	
	// Debug: log what we're trying to parse
	log.Printf("Debug: Attempting to parse JSON: %s", line)
	
	var event Event
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		return nil, fmt.Errorf("failed to parse event JSON '%s': %v", line, err)
	}
	
	return &event, nil
}

func (c *PubSubClient) Ping() error {
	if c.conn == nil {
		return fmt.Errorf("not connected")
	}
	_, err := c.conn.Write([]byte("PING\n"))
	return err
}

func (c *PubSubClient) Close() error {
	if c.conn == nil {
		return nil
	}
	c.conn.Write([]byte("QUIT\n"))
	return c.conn.Close()
}

// Simple reactive TUI that displays recent commands
type ReactiveTUI struct {
	client       *PubSubClient
	recentEvents []*Event
	maxEvents    int
}

func NewReactiveTUI(socketPath string, maxEvents int) *ReactiveTUI {
	return &ReactiveTUI{
		client:    NewPubSubClient(socketPath),
		maxEvents: maxEvents,
	}
}

func (tui *ReactiveTUI) Start() error {
	if err := tui.client.Connect(); err != nil {
		return err
	}
	defer tui.client.Close()
	
	// Subscribe to all events
	if err := tui.client.Subscribe("reactive-tui", ""); err != nil {
		return err
	}
	
	fmt.Printf("\033[2J\033[H") // Clear screen
	fmt.Println("🚀 Total Recall Reactive TUI")
	fmt.Println("Watching for shell commands...")
	fmt.Println(strings.Repeat("-", 80))
	
	// Handle Ctrl+C gracefully
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	
	go func() {
		<-sigChan
		fmt.Printf("\n\n👋 Shutting down TUI...\n")
		tui.client.Close()
		os.Exit(0)
	}()
	
	// Read and display events
	for {
		event, err := tui.client.ReadEvent()
		if err != nil {
			return fmt.Errorf("error reading event: %v", err)
		}
		
		tui.addEvent(event)
		tui.render()
	}
}

func (tui *ReactiveTUI) addEvent(event *Event) {
	tui.recentEvents = append(tui.recentEvents, event)
	
	// Keep only recent events
	if len(tui.recentEvents) > tui.maxEvents {
		tui.recentEvents = tui.recentEvents[1:]
	}
}

func (tui *ReactiveTUI) render() {
	// Move cursor to top and clear screen content
	fmt.Printf("\033[H")
	
	fmt.Printf("🚀 Total Recall Reactive TUI - %s\n", time.Now().Format("15:04:05"))
	fmt.Printf("Recent commands (last %d):\n", len(tui.recentEvents))
	fmt.Println(strings.Repeat("-", 80))
	
	if len(tui.recentEvents) == 0 {
		fmt.Println("No commands yet...")
		return
	}
	
	// Display recent events
	for i, event := range tui.recentEvents {
		duration := event.EndTimestamp.Sub(event.StartTimestamp)
		statusIcon := "✅"
		if event.ReturnCode != 0 {
			statusIcon = "❌"
		}
		
		// Truncate long commands
		command := event.Command
		if len(command) > 50 {
			command = command[:47] + "..."
		}
		
		// Truncate long paths
		pwd := event.Pwd
		if len(pwd) > 20 {
			parts := strings.Split(pwd, "/")
			if len(parts) > 2 {
				pwd = ".../" + strings.Join(parts[len(parts)-2:], "/")
			}
		}
		
		fmt.Printf("%2d. %s %-50s %20s (%4.0fms)\n", 
			i+1, statusIcon, command, pwd, float64(duration.Nanoseconds())/1000000)
	}
	
	// Show current stats
	fmt.Printf("\n📊 Stats: %d events displayed\n", len(tui.recentEvents))
	
	// Future enhancement suggestions
	if len(tui.recentEvents) >= 5 {
		tui.suggestCommands()
	}
}

func (tui *ReactiveTUI) suggestCommands() {
	fmt.Println("\n💡 Suggested commands based on current directory:")
	
	if len(tui.recentEvents) == 0 {
		return
	}
	
	lastEvent := tui.recentEvents[len(tui.recentEvents)-1]
	currentDir := lastEvent.Pwd
	
	// Analyze recent commands in this directory
	dirCommands := make(map[string]int)
	for _, event := range tui.recentEvents {
		if event.Pwd == currentDir && event.ReturnCode == 0 {
			dirCommands[event.Command]++
		}
	}
	
	// Simple heuristics for suggestions
	suggestions := []string{}
	
	// Check if it's a git repo
	if hasGitCommands := false; !hasGitCommands {
		for cmd := range dirCommands {
			if strings.Contains(cmd, "git") {
				hasGitCommands = true
				break
			}
		}
		if hasGitCommands {
			suggestions = append(suggestions, "git status", "git log --oneline -10")
		}
	}
	
	// Check for common development patterns
	if strings.Contains(currentDir, "src") || strings.Contains(currentDir, "code") {
		suggestions = append(suggestions, "ls -la", "find . -name '*.go' -o -name '*.py' -o -name '*.js'")
	}
	
	// Show suggestions
	for i, suggestion := range suggestions {
		if i >= 3 { // Limit to 3 suggestions
			break
		}
		fmt.Printf("   %d. %s\n", i+1, suggestion)
	}
}

// Test client for sending events
func testPublisher(socketPath string) error {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return err
	}
	defer conn.Close()
	
	// Send a test event
	testEvent := Event{
		Command:        "echo 'Hello from test publisher'",
		ReturnCode:     0,
		StartTimestamp: time.Now().Add(-time.Second),
		EndTimestamp:   time.Now(),
		Pwd:            "/tmp",
		Hostname:       "test-host",
	}
	
	data, _ := json.Marshal(testEvent)
	_, err = conn.Write(append(data, '\n'))
	
	fmt.Println("Sent test event to proxy")
	return err
}

func main() {
	var (
		socketPath = flag.String("socket", "/tmp/totalrecall-proxy.sock", "Unix domain socket path")
		mode       = flag.String("mode", "tui", "Mode: 'tui' for reactive TUI, 'test' for test publisher")
		maxEvents  = flag.Int("max-events", 20, "Maximum events to display in TUI")
		debug      = flag.Bool("debug", false, "Enable debug logging")
	)
	flag.Parse()
	
	// Set up logging
	if !*debug {
		log.SetOutput(io.Discard) // Disable debug logs unless explicitly enabled
	}
	
	switch *mode {
	case "tui":
		tui := NewReactiveTUI(*socketPath, *maxEvents)
		if err := tui.Start(); err != nil {
			log.Fatalf("TUI failed: %v", err)
		}
	case "test":
		if err := testPublisher(*socketPath); err != nil {
			log.Fatalf("Test publisher failed: %v", err)
		}
	default:
		fmt.Printf("Usage: %s -mode=[tui|test] [other options]\n", os.Args[0])
		flag.PrintDefaults()
	}
}
