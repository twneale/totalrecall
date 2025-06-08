package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/nats-io/nats.go"
	"github.com/rivo/tview"
)

// Event represents the structure of events from the NATS server
type Event struct {
	Environment struct {
		PWD string `json:"PWD"`
	} `json:"environment"`
	Timestamp string `json:"timestamp"`
}

// FileInfo holds information about a file or directory
type FileInfo struct {
	Name         string
	Path         string
	IsDir        bool
	Size         int64
	ModifiedTime time.Time
	Permissions  string // Added permissions field
}

// Global variables for communication
var dirChan = make(chan string, 10)

func main() {
	// Print startup message
	fmt.Println("Starting NATS Directory Watcher")

	// Start NATS client
	go func() {
		// Connect to NATS
		fmt.Println("Connecting to NATS...")
		nc, err := nats.Connect(nats.DefaultURL, nats.Timeout(5*time.Second))
		if err != nil {
			fmt.Printf("Error connecting to NATS: %v\n", err)
			return
		}
		defer nc.Close()

		// Subscribe to all messages
		_, err = nc.Subscribe("totalrecall", func(msg *nats.Msg) {

			// Try to parse the message
			var event Event
			if err := json.Unmarshal(msg.Data, &event); err != nil {
				fmt.Printf("Error parsing message: %v\n", err)
				return
			}

			// Check for PWD field
			if event.Environment.PWD == "" {
				fmt.Println("No PWD field in message")
				return
			}

			dirChan <- event.Environment.PWD
		})

		if err != nil {
			fmt.Printf("Error subscribing to NATS: %v\n", err)
			return
		}

		// Keep goroutine alive
		for {
			time.Sleep(time.Second)
		}
	}()

	// Create application
	app := tview.NewApplication()

	// Create basic views
	header := tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignCenter)
	table := tview.NewTable().SetBorders(false).SetSelectable(true, false)

	// Set up basic layout
	flex := tview.NewFlex().SetDirection(tview.FlexRow)
	flex.AddItem(header, 1, 0, false)
	flex.AddItem(table, 0, 1, true)

	// Set initial header
	fmt.Fprintln(header, "Waiting for events...")

	// Set ESC handler
	app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEscape {
			app.Stop()
			return nil
		}
		return event
	})

	// Watch for directory changes
	go func() {
		for dir := range dirChan {

			app.QueueUpdateDraw(func() {

				// Update header
				header.Clear()
				fmt.Fprintf(header, "[green]%s[white]\n", dir)

				// Clear table
				table.Clear()

				// List files
				files, err := getFileList(dir)
				if err != nil {
					fmt.Printf("Error listing directory: %v\n", err)
					table.SetCell(1, 0, tview.NewTableCell(fmt.Sprintf("Error: %v", err)).
						SetTextColor(tcell.ColorRed).
						SetExpansion(1))
					return
				}

				// Sort files
				sort.Slice(files, func(i, j int) bool {
					return files[i].ModifiedTime.After(files[j].ModifiedTime)
				})

				// Add files to table
				for i, file := range files {
					// Color based on type
					cellColor := tcell.ColorWhite
					if file.IsDir {
						cellColor = tcell.ColorBlue
					} else if isExecutable(file.Path) {
						cellColor = tcell.ColorGreen
					}

					// Add cells - modified to show only permissions, date, and name with padding
					table.SetCell(i, 0, tview.NewTableCell(fmt.Sprintf(" %s ", file.Permissions)))

					// Modified time with ls -lath format
					table.SetCell(i, 1, tview.NewTableCell(fmt.Sprintf(" %s ", formatLsTime(file.ModifiedTime))))

					// Name
					table.SetCell(i, 2, tview.NewTableCell(fmt.Sprintf(" %s", file.Name)).
						SetTextColor(cellColor))
				}
				
				// Make sure table is scrolled to the top after updating
				table.ScrollToBeginning()
			})
		}
	}()

	// Start the application
	fmt.Println("Starting UI...")
	if err := app.SetRoot(flex, true).Run(); err != nil {
		fmt.Printf("Error running application: %v\n", err)
		os.Exit(1)
	}
}

// getFileList returns a list of files and directories at the given path
func getFileList(path string) ([]FileInfo, error) {
	dir, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer dir.Close()

	entries, err := dir.Readdir(-1)
	if err != nil {
		return nil, err
	}

	var files []FileInfo
	for _, entry := range entries {
		// Skip hidden files that start with "."
		if len(entry.Name()) > 0 && entry.Name()[0] == '.' {
			continue
		}

		// Get file info to extract permissions
		fileInfo, err := os.Stat(filepath.Join(path, entry.Name()))
		if err != nil {
			continue
		}

		file := FileInfo{
			Name:         entry.Name(),
			Path:         filepath.Join(path, entry.Name()),
			IsDir:        entry.IsDir(),
			Size:         entry.Size(),
			ModifiedTime: entry.ModTime(),
			Permissions:  formatPermissions(fileInfo.Mode()), // Format permissions
		}
		files = append(files, file)
	}

	return files, nil
}

// formatPermissions formats file permissions like ls -l
func formatPermissions(mode os.FileMode) string {
	// Start with file type indicator
	var permissions string
	if mode&os.ModeDir != 0 {
		permissions = "d"
	} else if mode&os.ModeSymlink != 0 {
		permissions = "l"
	} else {
		permissions = "-"
	}

	// Add user permissions
	permissions += formatPermission(mode, 0400, 0200, 0100)
	// Add group permissions
	permissions += formatPermission(mode, 0040, 0020, 0010)
	// Add other permissions
	permissions += formatPermission(mode, 0004, 0002, 0001)

	return permissions
}

// formatPermission formats read, write, execute permissions for a set
func formatPermission(mode os.FileMode, r, w, x os.FileMode) string {
	perms := ""
	if mode&r != 0 {
		perms += "r"
	} else {
		perms += "-"
	}
	if mode&w != 0 {
		perms += "w"
	} else {
		perms += "-"
	}
	if mode&x != 0 {
		perms += "x"
	} else {
		perms += "-"
	}
	return perms
}

// formatLsTime formats time like ls -l does
func formatLsTime(t time.Time) string {
	now := time.Now()
	sixMonthsAgo := now.AddDate(0, -6, 0)

	// If the file was modified less than 6 months ago, show "Mon DD HH:MM"
	if t.After(sixMonthsAgo) {
		return t.Format("Jan _2 15:04")
	}

	// If the file was modified more than 6 months ago, show "Mon DD YYYY"
	return t.Format("Jan _2  2006")
}

// isExecutable checks if a file is executable
func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir() && info.Mode()&0111 != 0
}
