package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

// PreexecData holds all the data we need to collect before command execution
type PreexecData struct {
	CommandID      string    `json:"command_id"`
	Command        string    `json:"command"`
	Pwd            string    `json:"pwd"`
	StartTimestamp time.Time `json:"start_timestamp"`
	Environment    []string  `json:"environment"`
}

// PubSubEvent for sending to TLS proxy pub/sub (preexec and precmd events)
type PubSubEvent struct {
	EventType      string                 `json:"event_type"`
	CommandID      string                 `json:"command_id"`
	Command        string                 `json:"command,omitempty"`
	Pwd            string                 `json:"pwd,omitempty"`
	StartTimestamp *time.Time             `json:"start_timestamp,omitempty"`
	EndTimestamp   *time.Time             `json:"end_timestamp,omitempty"`
	ReturnCode     *int                   `json:"return_code,omitempty"`
	Environment    map[string]string      `json:"environment,omitempty"`
	PubSubOnly     bool                   `json:"pubsub_only"` // Tells TLS proxy: don't send to fluent-bit
}

func generateCommandID() string {
	bytes := make([]byte, 4)
	rand.Read(bytes)
	return fmt.Sprintf("%d_%s", time.Now().UnixMilli(), hex.EncodeToString(bytes))
}

func main() {
	// Check for special pub/sub modes
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "--send-preexec-event":
			sendPreexecEvent()
			return
		case "--send-precmd-event":
			sendPrecmdEvent()
			return
		}
	}
	
	// Original behavior: collect data and return base64 blob for shell storage
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <command>\n", os.Args[0])
		os.Exit(1)
	}
	
	command := os.Args[1]
	commandID := generateCommandID()
	
	// Gather all data in one go
	data := PreexecData{
		CommandID:      commandID,
		Command:        command,
		Pwd:            getPwd(),
		StartTimestamp: time.Now(),
		Environment:    getFilteredEnvironment(),
	}
	
	// Marshal to JSON
	jsonData, err := json.Marshal(data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error marshaling data: %v\n", err)
		os.Exit(1)
	}
	
	// Base64 encode for safe shell transport
	encoded := base64.StdEncoding.EncodeToString(jsonData)
	fmt.Print(encoded)
}

func sendPreexecEvent() {
	// Read preexec data from environment variable
	preexecData := os.Getenv("___PREEXEC_DATA")
	if preexecData == "" {
		fmt.Fprintf(os.Stderr, "No preexec data found in ___PREEXEC_DATA\n")
		os.Exit(1)
	}
	
	// Decode the base64 data
	decoded, err := base64.StdEncoding.DecodeString(preexecData)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to decode preexec data: %v\n", err)
		os.Exit(1)
	}
	
	var data PreexecData
	if err := json.Unmarshal(decoded, &data); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to parse preexec data: %v\n", err)
		os.Exit(1)
	}
	
	// Convert environment slice to map (simplified filtering for pub/sub)
	env := make(map[string]string)
	for _, envVar := range data.Environment {
		parts := strings.SplitN(envVar, "=", 2)
		if len(parts) == 2 {
			// Basic filtering - you can enhance this with the env_config.go logic
			key := parts[0]
			if !shouldSkipForPubSub(key) {
				env[key] = parts[1]
			}
		}
	}
	
	// Create preexec pub/sub event
	event := PubSubEvent{
		EventType:      "preexec",
		CommandID:      data.CommandID,
		Command:        data.Command,
		Pwd:            data.Pwd,
		StartTimestamp: &data.StartTimestamp,
		Environment:    env,
		PubSubOnly:     true, // Don't send to fluent-bit
	}
	
	// Send to TLS proxy pub/sub
	sendToPubSub(event)
}

func sendPrecmdEvent() {
	// Get command ID and return code from environment
	commandID := os.Getenv("___COMMAND_ID")
	returnCodeStr := os.Getenv("___RETURN_CODE")
	
	if commandID == "" {
		fmt.Fprintf(os.Stderr, "Missing command ID in ___COMMAND_ID\n")
		os.Exit(1)
	}
	
	if returnCodeStr == "" {
		fmt.Fprintf(os.Stderr, "Missing return code in ___RETURN_CODE\n")
		os.Exit(1)
	}
	
	returnCode := 0
	fmt.Sscanf(returnCodeStr, "%d", &returnCode)
	
	endTime := time.Now()
	
	// Create precmd pub/sub event
	event := PubSubEvent{
		EventType:    "precmd",
		CommandID:    commandID,
		ReturnCode:   &returnCode,
		EndTimestamp: &endTime,
		PubSubOnly:   true, // Don't send to fluent-bit
	}
	
	// Send to TLS proxy pub/sub
	sendToPubSub(event)
}

func sendToPubSub(event PubSubEvent) {
	socketPath := "/tmp/totalrecall-proxy.sock"
	
	// Connect to unix socket (non-blocking approach)
	conn, err := net.DialTimeout("unix", socketPath, 100*time.Millisecond)
	if err != nil {
		// Silently fail - don't block shell execution if proxy is down
		return
	}
	defer conn.Close()
	
	// Marshal event to JSON
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	
	// Send to socket with short timeout
	conn.SetWriteDeadline(time.Now().Add(50 * time.Millisecond))
	conn.Write(append(data, '\n'))
}

func shouldSkipForPubSub(key string) bool {
	// Skip noisy variables for pub/sub events (keep it lightweight)
	skipPrefixes := []string{
		"___", "_=", "PS1", "PS2", "BASH_", "HIST", "LESS", "LS_COLORS", "TERM",
	}
	
	for _, prefix := range skipPrefixes {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

func getPwd() string {
	pwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return pwd
}

func getFilteredEnvironment() []string {
	env := os.Environ()
	filtered := make([]string, 0, len(env))
	
	for _, envVar := range env {
		// Skip our temporary preexec variables and shell internals
		if strings.HasPrefix(envVar, "___PREEXEC_") ||
		   strings.HasPrefix(envVar, "_=") ||
		   strings.HasPrefix(envVar, "PS1=") ||
		   strings.HasPrefix(envVar, "PS2=") ||
		   strings.HasPrefix(envVar, "BASH_") ||
		   strings.HasPrefix(envVar, "FUNCNAME=") ||
		   strings.HasPrefix(envVar, "PIPESTATUS=") {
			continue
		}
		filtered = append(filtered, envVar)
	}
	
	return filtered
}
