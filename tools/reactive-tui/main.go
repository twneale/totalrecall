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
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// Enhanced event structure to handle both preexec and precmd
type EnhancedEvent struct {
	// Common fields
	EventType   string `json:"event_type"` // "preexec", "precmd", or regular event
	CommandID   string `json:"command_id,omitempty"`
	PubSubOnly  bool   `json:"pubsub_only,omitempty"`
	
	// Preexec fields
	Command        string                 `json:"command,omitempty"`
	Pwd            string                 `json:"pwd,omitempty"`
	StartTimestamp *time.Time             `json:"start_timestamp,omitempty"`
	Environment    map[string]string      `json:"env,omitempty"`
	
	// Precmd fields
	ReturnCode     *int       `json:"return_code,omitempty"`
	EndTimestamp   *time.Time `json:"end_timestamp,omitempty"`
	
	// Legacy fields for backwards compatibility
	Hostname       string `json:"hostname,omitempty"`
	IPAddress      string `json:"ip_address,omitempty"`
}

// CommandState represents the state of a command in the UI
type CommandState struct {
	CommandID      string
	Command        string
	Pwd            string
	StartTime      time.Time
	EndTime        *time.Time
	ReturnCode     *int
	Environment    map[string]string
	Status         string // "pending", "success", "error"
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

func (c *PubSubClient) ReadEvent() (*EnhancedEvent, error) {
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
	
	var event EnhancedEvent
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		return nil, fmt.Errorf("failed to parse event JSON '%s': %v", line, err)
	}
	
	return &event, nil
}

func (c *PubSubClient) Close() error {
	if c.conn == nil {
		return nil
	}
	c.conn.Write([]byte("QUIT\n"))
	return c.conn.Close()
}

// Enhanced reactive TUI that correlates preexec/precmd events
type EnhancedReactiveTUI struct {
	client            *PubSubClient
	commandStates     map[string]*CommandState // keyed by command_id
	recentCommands    []*CommandState          // ordered list for display
	maxEvents         int
	recentCorrelations map[string]time.Time   // track recent correlation completions to avoid duplicates
}

func NewEnhancedReactiveTUI(socketPath string, maxEvents int) *EnhancedReactiveTUI {
	return &EnhancedReactiveTUI{
		client:            NewPubSubClient(socketPath),
		commandStates:     make(map[string]*CommandState),
		recentCommands:    make([]*CommandState, 0),
		maxEvents:         maxEvents,
		recentCorrelations: make(map[string]time.Time),
	}
}

func (tui *EnhancedReactiveTUI) Start() error {
	if err := tui.client.Connect(); err != nil {
		return err
	}
	defer tui.client.Close()
	
	// Subscribe to all events
	if err := tui.client.Subscribe("reactive-tui", ""); err != nil {
		return err
	}
	
	fmt.Printf("\033[2J\033[H") // Clear screen
	fmt.Println("🚀 Total Recall Enhanced Reactive TUI")
	fmt.Println("Real-time command tracking with preexec/precmd correlation")
	fmt.Println(strings.Repeat("-", 80))
	
	// Handle Ctrl+C gracefully
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	
	go func() {
		<-sigChan
		fmt.Printf("\n\n👋 Shutting down Enhanced TUI...\n")
		tui.client.Close()
		os.Exit(0)
	}()
	
	// Read and process events
	for {
		event, err := tui.client.ReadEvent()
		if err != nil {
			return fmt.Errorf("error reading event: %v", err)
		}
		
		tui.processEvent(event)
		tui.render()
	}
}

func (tui *EnhancedReactiveTUI) processEvent(event *EnhancedEvent) {
	switch event.EventType {
	case "preexec":
		tui.handlePreexecEvent(event)
	case "precmd":
		tui.handlePrecmdEvent(event)
	default:
		// Handle legacy events, but avoid duplicates
		tui.handleLegacyEvent(event)
	}
}

func (tui *EnhancedReactiveTUI) handlePreexecEvent(event *EnhancedEvent) {
	if event.CommandID == "" {
		log.Printf("Warning: preexec event missing command_id")
		return
	}
	
	commandState := &CommandState{
		CommandID:   event.CommandID,
		Command:     event.Command,
		Pwd:         event.Pwd,
		StartTime:   time.Now(), // Use current time if start time not provided
		Environment: event.Environment,
		Status:      "pending",
	}
	
	if event.StartTimestamp != nil {
		commandState.StartTime = *event.StartTimestamp
	}
	
	// Store and add to recent commands
	tui.commandStates[event.CommandID] = commandState
	tui.addCommandToRecent(commandState)
	
	log.Printf("📝 Preexec: %s (ID: %s)", event.Command, event.CommandID)
}

func (tui *EnhancedReactiveTUI) handlePrecmdEvent(event *EnhancedEvent) {
	if event.CommandID == "" {
		log.Printf("Warning: precmd event missing command_id")
		return
	}
	
	commandState, exists := tui.commandStates[event.CommandID]
	if !exists {
		log.Printf("Warning: precmd event for unknown command_id: %s", event.CommandID)
		return
	}
	
	// Update command state with precmd data
	commandState.ReturnCode = event.ReturnCode
	if event.EndTimestamp != nil {
		commandState.EndTime = event.EndTimestamp
	} else {
		now := time.Now()
		commandState.EndTime = &now
	}
	
	// Update status based on return code
	if event.ReturnCode != nil {
		if *event.ReturnCode == 0 {
			commandState.Status = "success"
		} else {
			commandState.Status = "error"
		}
	}
	
	// Mark this correlation as recently completed (to avoid legacy duplicates)
	commandKey := fmt.Sprintf("%s_%s", commandState.Command, commandState.Pwd)
	tui.recentCorrelations[commandKey] = time.Now()
	
	log.Printf("✅ Precmd: ID %s completed with return code %d", event.CommandID, *event.ReturnCode)
}

func (tui *EnhancedReactiveTUI) handleLegacyEvent(event *EnhancedEvent) {
	// Skip legacy events if we recently processed a correlation event for the same command
	if event.Command != "" && event.Pwd != "" {
		commandKey := fmt.Sprintf("%s_%s", event.Command, event.Pwd)
		if recentTime, exists := tui.recentCorrelations[commandKey]; exists {
			// Only skip if the correlation event completed within the last 3 seconds
			if time.Since(recentTime) < 3*time.Second {
				log.Printf("Skipping duplicate legacy event for: %s (recent correlation)", event.Command)
				return
			}
			// Clean up old entries to prevent memory leaks
			if time.Since(recentTime) > 10*time.Second {
				delete(tui.recentCorrelations, commandKey)
			}
		}
	}
	
	// Handle old-style events that don't have command correlation
	if event.Command != "" {
		commandState := &CommandState{
			CommandID:  fmt.Sprintf("legacy_%d", time.Now().UnixNano()),
			Command:    event.Command,
			Pwd:        event.Pwd,
			StartTime:  time.Now(),
			Status:     "success", // Assume success for legacy events
		}
		
		if event.ReturnCode != nil {
			commandState.ReturnCode = event.ReturnCode
			if *event.ReturnCode != 0 {
				commandState.Status = "error"
			}
		}
		
		tui.addCommandToRecent(commandState)
		log.Printf("📄 Legacy: %s", event.Command)
	}
}

func (tui *EnhancedReactiveTUI) addCommandToRecent(commandState *CommandState) {
	tui.recentCommands = append(tui.recentCommands, commandState)
	
	// Keep only recent commands
	if len(tui.recentCommands) > tui.maxEvents {
		tui.recentCommands = tui.recentCommands[1:]
	}
}

func (tui *EnhancedReactiveTUI) render() {
	// Clear screen and move cursor to top to prevent scrolling issues
	fmt.Printf("\033[2J\033[H")
	
	fmt.Printf("🚀 Total Recall Enhanced TUI - %s\n", time.Now().Format("15:04:05"))
	fmt.Printf("Recent commands (last %d) - Live correlation active:\n", len(tui.recentCommands))
	fmt.Println(strings.Repeat("-", 80))
	
	if len(tui.recentCommands) == 0 {
		fmt.Println("No commands yet... waiting for shell activity")
		return
	}
	
	// Get terminal height to limit display and prevent scrolling
	// Reserve 4 lines for header, so show at most (height - 4) commands
	maxDisplayLines := tui.getTerminalHeight() - 4
	if maxDisplayLines < 5 {
		maxDisplayLines = 5 // minimum reasonable display
	}
	
	// Display recent commands with status-based coloring (reverse order - most recent first)
	displayCount := 0
	for i := len(tui.recentCommands) - 1; i >= 0 && displayCount < maxDisplayLines; i-- {
		cmd := tui.recentCommands[i]
		
		duration := ""
		statusIcon := tui.getStatusIcon(cmd)
		statusColor := tui.getStatusColor(cmd)
		
		if cmd.EndTime != nil {
			duration = fmt.Sprintf("(%4.0fms)", float64(cmd.EndTime.Sub(cmd.StartTime).Nanoseconds())/1000000)
		} else {
			duration = "(running...)"
		}
		
		// Format start time as HH:MM:SS
		timeStr := cmd.StartTime.Format("15:04:05")
		
		// Truncate long commands
		command := cmd.Command
		if len(command) > 45 {
			command = command[:42] + "..."
		}
		
		// Truncate long paths
		pwd := cmd.Pwd
		if len(pwd) > 20 {
			parts := strings.Split(pwd, "/")
			if len(parts) > 2 {
				pwd = ".../" + strings.Join(parts[len(parts)-2:], "/")
			}
		}
		
		// Build the display line
		displayLine := fmt.Sprintf("%s %s %-45s %20s %s", 
			timeStr, statusIcon, command, pwd, duration)
		
		// Add return code if command completed
		if cmd.ReturnCode != nil {
			displayLine += fmt.Sprintf(" [%d]", *cmd.ReturnCode)
		}
		
		// Print line and clear to end of line to avoid display corruption
		fmt.Printf("%s%s\033[K\033[0m\n", statusColor, displayLine)
		displayCount++
	}
}

func (tui *EnhancedReactiveTUI) getTerminalHeight() int {
	// Try to get terminal size
	if height, err := getTerminalSize(); err == nil {
		return height
	}
	// Fallback to reasonable default
	return 25
}

// Simple function to get terminal size (height)
func getTerminalSize() (int, error) {
	// Try using tput command for height
	cmd := exec.Command("tput", "lines")
	output, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	
	var height int
	fmt.Sscanf(string(output), "%d", &height)
	return height, nil
}

func (tui *EnhancedReactiveTUI) getStatusIcon(cmd *CommandState) string {
	switch cmd.Status {
	case "pending":
		return "⏳" // Hourglass for running commands
	case "success":
		return "✅" // Green check for successful commands  
	case "error":
		return "❌" // Red X for failed commands
	default:
		return "❓" // Question mark for unknown status
	}
}

func (tui *EnhancedReactiveTUI) getStatusColor(cmd *CommandState) string {
	switch cmd.Status {
	case "pending":
		return "\033[90m"  // Grey for pending
	case "success":
		return "\033[92m"  // Bright green for success
	case "error":
		return "\033[91m"  // Bright red for error
	default:
		return "\033[0m"   // Default color
	}
}

// Test client for sending test events
func testCorrelationPublisher(socketPath string) error {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return err
	}
	defer conn.Close()
	
	// Send a test preexec event
	commandID := fmt.Sprintf("test_%d", time.Now().UnixMilli())
	
	preexecEvent := EnhancedEvent{
		EventType:      "preexec",
		CommandID:      commandID,
		Command:        "echo 'Testing correlation'",
		Pwd:            "/tmp",
		StartTimestamp: &[]time.Time{time.Now()}[0],
		PubSubOnly:     true,
	}
	
	data, _ := json.Marshal(preexecEvent)
	conn.Write(append(data, '\n'))
	
	fmt.Printf("Sent preexec event: %s\n", commandID)
	
	// Wait a bit, then send precmd event
	time.Sleep(2 * time.Second)
	
	precmdEvent := EnhancedEvent{
		EventType:    "precmd",
		CommandID:    commandID,
		ReturnCode:   &[]int{0}[0],
		EndTimestamp: &[]time.Time{time.Now()}[0],
		PubSubOnly:   true,
	}
	
	data, _ = json.Marshal(precmdEvent)
	conn.Write(append(data, '\n'))
	
	fmt.Printf("Sent precmd event: %s\n", commandID)
	return nil
}

func main() {
	var (
		socketPath = flag.String("socket", "/tmp/totalrecall-proxy.sock", "Unix domain socket path")
		mode       = flag.String("mode", "tui", "Mode: 'tui' for reactive TUI, 'test' for correlation test")
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
		tui := NewEnhancedReactiveTUI(*socketPath, *maxEvents)
		if err := tui.Start(); err != nil {
			log.Fatalf("Enhanced TUI failed: %v", err)
		}
	case "test":
		if err := testCorrelationPublisher(*socketPath); err != nil {
			log.Fatalf("Correlation test failed: %v", err)
		}
	default:
		fmt.Printf("Usage: %s -mode=[tui|test] [other options]\n", os.Args[0])
		flag.PrintDefaults()
	}
}
