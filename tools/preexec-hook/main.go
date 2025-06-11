package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// PreexecData holds all the data we need to collect before command execution
type PreexecData struct {
	Command        string    `json:"command"`
	Pwd            string    `json:"pwd"`
	StartTimestamp time.Time `json:"start_timestamp"`
	Environment    []string  `json:"environment"`
}

func main() {
	// Get command from arguments
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <command>\n", os.Args[0])
		os.Exit(1)
	}
	
	command := os.Args[1]
	
	// Gather all data in one go
	data := PreexecData{
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
