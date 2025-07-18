package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Command represents a command from elasticsearch
type Command struct {
	ID             string            `json:"_id"`
	Command        string            `json:"command"`
	StartTimestamp time.Time         `json:"start_timestamp"`
	EndTimestamp   time.Time         `json:"end_timestamp"`
	ReturnCode     int               `json:"return_code"`
	Pwd            string            `json:"pwd"`
	Hostname       string            `json:"hostname"`
	ShellPid       int               `json:"shellpid"`
	Environment    map[string]string `json:"env,omitempty"`
}

// QueryResponse represents elasticsearch response
type QueryResponse struct {
	Commands []Command `json:"commands"`
	Total    int       `json:"total"`
	HasMore  bool      `json:"has_more"`
}

// Styles
var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#7C3AED")).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#7C3AED")).
			Padding(0, 1)

	focusedStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(lipgloss.Color("#7C3AED")).
			Padding(0, 1)

	successStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#10B981"))

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#EF4444"))

	warningStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F59E0B"))

	mutedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#6B7280"))

	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#9CA3AF"))

	searchStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#7C3AED")).
			Padding(0, 1)

	detailStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#10B981")).
			Padding(1)

	filterIndicatorStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#F59E0B")).
				Bold(true)

	tableStyle = lipgloss.NewStyle().
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color("#7C3AED"))
)

// Key bindings
type keyMap struct {
	Up         key.Binding
	Down       key.Binding
	PageUp     key.Binding
	PageDown   key.Binding
	Edit       key.Binding
	Copy       key.Binding
	Delete     key.Binding
	Help       key.Binding
	Enter      key.Binding
	Execute    key.Binding
	Search     key.Binding
	Fzf        key.Binding
	ToggleHost key.Binding
	ToggleShell key.Binding
	TogglePwd  key.Binding
	LoadMore   key.Binding
	Escape     key.Binding
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Help, k.Quit}
}

func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.PageUp, k.PageDown},
		{k.Enter, k.Edit, k.Execute, k.Copy},
		{k.Search, k.Fzf, k.Delete, k.LoadMore},
		{k.ToggleHost, k.ToggleShell, k.TogglePwd},
		{k.Help, k.Escape, k.Quit},
	}
}

var keys = keyMap{
	Up: key.NewBinding(
		key.WithKeys("k", "up"),
		key.WithHelp("k/↑", "up"),
	),
	Down: key.NewBinding(
		key.WithKeys("j", "down"),
		key.WithHelp("j/↓", "down"),
	),
	PageUp: key.NewBinding(
		key.WithKeys("b", "ctrl+up"),
		key.WithHelp("b", "page up"),
	),
	PageDown: key.NewBinding(
		key.WithKeys("f", "ctrl+down", "space"),
		key.WithHelp("f/space", "page down"),
	),
	Edit: key.NewBinding(
		key.WithKeys("e"),
		key.WithHelp("e", "edit command"),
	),
	Copy: key.NewBinding(
		key.WithKeys("c"),
		key.WithHelp("c", "copy command"),
	),
	Delete: key.NewBinding(
		key.WithKeys("d"),
		key.WithHelp("d", "delete entry"),
	),
	Help: key.NewBinding(
		key.WithKeys("?"),
		key.WithHelp("?", "toggle help"),
	),
	Enter: key.NewBinding(
		key.WithKeys("enter", "l"),
		key.WithHelp("enter/l", "view details"),
	),
	Execute: key.NewBinding(
		key.WithKeys("x"),
		key.WithHelp("x", "execute command"),
	),
	Search: key.NewBinding(
		key.WithKeys("/"),
		key.WithHelp("/", "search"),
	),
	Fzf: key.NewBinding(
		key.WithKeys("ctrl+f"),
		key.WithHelp("ctrl+f", "fuzzy find"),
	),
	ToggleHost: key.NewBinding(
		key.WithKeys("h"),
		key.WithHelp("h", "toggle host column"),
	),
	ToggleShell: key.NewBinding(
		key.WithKeys("s"),
		key.WithHelp("s", "toggle shell column"),
	),
	TogglePwd: key.NewBinding(
		key.WithKeys("p"),
		key.WithHelp("p", "toggle pwd column"),
	),
	LoadMore: key.NewBinding(
		key.WithKeys("m"),
		key.WithHelp("m", "load more (debug)"),
	),
	Quit: key.NewBinding(
		key.WithKeys("q", "ctrl+c"),
		key.WithHelp("q", "quit"),
	),
	Escape: key.NewBinding(
		key.WithKeys("esc"),
		key.WithHelp("esc", "back"),
	),
}

// ViewState represents the current view
type ViewState int

const (
	ViewList ViewState = iota
	ViewDetail
	ViewSearch
	ViewHelp
	ViewConfirm
)

// Model represents the TUI model
type Model struct {
	// State
	state       ViewState
	width       int
	height      int
	
	// Data
	commands    []Command
	cursor      int
	totalCount  int
	hasMore     bool
	
	// Filters and search
	searchQuery string
	showHost    bool
	showShell   bool
	showPwd     bool
	
	// Current context for filtering
	currentPwd  string
	currentHost string
	parentPid   int
	
	// UI Components
	searchInput textinput.Model
	table       table.Model
	viewport    viewport.Model
	help        help.Model
	
	// Table configuration
	tableColumns []table.Column
	
	// Confirmation state
	confirmAction string
	confirmTarget string
	
	// Socket connection
	socketPath string
	
	// Loading state
	loading bool
	
	// Scroll position
	scrollOffset int
}

// Messages
type commandsLoadedMsg struct {
	commands []Command
	total    int
	hasMore  bool
	append   bool // Whether to append to existing commands or replace them
}

type commandDeletedMsg struct {
	id string
}

type errorMsg struct {
	err error
}

type vimFinishedMsg struct{}

// Initialize the model
func initialModel(socketPath string) Model {
	ti := textinput.New()
	ti.Placeholder = "Search commands..."
	ti.CharLimit = 100

	// Initialize table with basic columns
	columns := []table.Column{
		{Title: "Time", Width: 8},
		{Title: "Command", Width: 50},
		{Title: "Status", Width: 6},
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithFocused(true),
		table.WithHeight(20),
	)

	t.SetStyles(table.Styles{
		Header: lipgloss.NewStyle().
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color("#7C3AED")).
			BorderBottom(true).
			Bold(true).
			Padding(0, 1), // Add horizontal padding
		Selected: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(lipgloss.Color("#7C3AED")).
			Bold(true).
			Padding(0, 1), // Add horizontal padding
		Cell: lipgloss.NewStyle().
			Padding(0, 1), // Add horizontal padding to all cells
	})

	vp := viewport.New(80, 20)
	
	// Get current context
	pwd, _ := os.Getwd()
	hostname, _ := os.Hostname()
	parentPid := os.Getppid()

	return Model{
		state:       ViewList,
		commands:    []Command{},
		cursor:      0,
		searchInput: ti,
		table:       t,
		tableColumns: columns,
		viewport:    vp,
		help:        help.New(),
		currentPwd:  pwd,
		currentHost: hostname,
		parentPid:   parentPid,
		socketPath:  socketPath,
		// Start with basic view
		showHost:  false,
		showShell: false,
		showPwd:   false,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		textinput.Blink,
		m.loadCommands(0, 50), // Load first page
	)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.viewport.Width = msg.Width - 4
		m.viewport.Height = msg.Height - 8 // Reserve space for header/footer
		m.updateTableSize()
		
		// If we have commands, update table data
		if len(m.commands) > 0 {
			m.updateTableData()
		}
		
	case commandsLoadedMsg:
		fmt.Fprintf(os.Stderr, "DEBUG: Loaded %d commands, total=%d, hasMore=%t, append=%t\n",
			len(msg.commands), msg.total, msg.hasMore, msg.append)
			
		if msg.append {
			// Append new commands to existing list
			m.commands = append(m.commands, msg.commands...)
		} else {
			// Replace commands (new search/filter)
			m.commands = msg.commands
		}
		
		m.totalCount = msg.total
		m.hasMore = msg.hasMore
		m.loading = false
		m.updateTableData()
		
		// Ensure cursor is valid
		if len(m.commands) > 0 && m.cursor >= len(m.commands) {
			m.cursor = len(m.commands) - 1
		}
		
		fmt.Fprintf(os.Stderr, "DEBUG: After load - len(commands)=%d, hasMore=%t\n", 
			len(m.commands), m.hasMore)
		
	case commandDeletedMsg:
		// Remove deleted command from list
		for i, cmd := range m.commands {
			if cmd.ID == msg.id {
				m.commands = append(m.commands[:i], m.commands[i+1:]...)
				if m.cursor >= len(m.commands) && len(m.commands) > 0 {
					m.cursor = len(m.commands) - 1
				}
				m.updateTableData()
				break
			}
		}
		
	case vimFinishedMsg:
		// Vim finished, return to previous state
		return m, nil
		
	case errorMsg:
		// Handle errors (could show in status bar)
		m.loading = false
		
	case tea.KeyMsg:
		switch m.state {
		case ViewList:
			return m.updateList(msg)
		case ViewDetail:
			return m.updateDetail(msg)
		case ViewSearch:
			return m.updateSearch(msg)
		case ViewHelp:
			return m.updateHelp(msg)
		case ViewConfirm:
			return m.updateConfirm(msg)
		}
	}
	
	return m, cmd
}

func (m *Model) updateTableSize() {
	// Calculate column widths based on window size and visible columns
	availableWidth := m.width - 10 // Leave margin for borders
	
	columns := []table.Column{
		{Title: "Time", Width: 8},
	}
	
	remainingWidth := availableWidth - 8 // Time column
	
	// Add optional columns
	if m.showHost {
		columns = append(columns, table.Column{Title: "Host", Width: 15})
		remainingWidth -= 15
	}
	
	if m.showShell {
		columns = append(columns, table.Column{Title: "PID", Width: 8})
		remainingWidth -= 8
	}
	
	if m.showPwd {
		pwdWidth := 25
		if remainingWidth < 50 { // If space is tight
			pwdWidth = 20
		}
		columns = append(columns, table.Column{Title: "PWD", Width: pwdWidth})
		remainingWidth -= pwdWidth
	}
	
	// Command column gets remaining space (minimum 30)
	cmdWidth := max(30, remainingWidth-10) // Leave space for status
	columns = append(columns, table.Column{Title: "Command", Width: cmdWidth})
	columns = append(columns, table.Column{Title: "Status", Width: 8})
	
	// Store columns and update table
	m.tableColumns = columns
	m.table.SetColumns(columns)
	m.table.SetHeight(min(20, m.height-8))
	
	// Clear any existing rows to prevent column mismatch issues
	m.table.SetRows([]table.Row{})
}

func (m *Model) updateTableData() {
	rows := []table.Row{}
	
	for _, cmd := range m.commands {
		row := table.Row{}
		
		// Time
		row = append(row, cmd.StartTimestamp.Format("15:04:05"))
		
		// Optional columns
		if m.showHost {
			row = append(row, truncateString(cmd.Hostname, 14))
		}
		
		if m.showShell {
			row = append(row, strconv.Itoa(cmd.ShellPid))
		}
		
		if m.showPwd {
			// Show last 32 chars of PWD
			pwd := cmd.Pwd
			if len(pwd) > 32 {
				pwd = "..." + pwd[len(pwd)-29:]
			}
			row = append(row, pwd)
		}
		
		// Command (truncated) - always second to last column
		command := cmd.Command
		cmdWidth := 50 // Default width
		
		// Calculate command column width safely
		if len(m.tableColumns) >= 2 {
			// Command column is always second to last
			cmdColumnIndex := len(m.tableColumns) - 2
			if cmdColumnIndex >= 0 && cmdColumnIndex < len(m.tableColumns) {
				cmdWidth = m.tableColumns[cmdColumnIndex].Width
			}
		}
		
		if len(command) > cmdWidth {
			command = command[:cmdWidth-3] + "..."
		}
		row = append(row, command)
		
		// Status - always last column
		status := "✅"
		if cmd.ReturnCode != 0 {
			status = "❌"
		}
		row = append(row, status)
		
		rows = append(rows, row)
	}
	
	m.table.SetRows(rows)
}

func (m Model) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	
	switch {
	case key.Matches(msg, keys.Quit):
		return m, tea.Quit
		
	case key.Matches(msg, keys.Up):
		m.table, cmd = m.table.Update(msg)
		m.cursor = m.table.Cursor()
		
	case key.Matches(msg, keys.Down):
		m.table, cmd = m.table.Update(msg)
		m.cursor = m.table.Cursor()
		
		// Debug logging - remove this later
		fmt.Fprintf(os.Stderr, "DEBUG: cursor=%d, len(commands)=%d, hasMore=%t, loading=%t\n", 
			m.cursor, len(m.commands), m.hasMore, m.loading)
		
		// More aggressive infinite scroll check
		isNearBottom := m.cursor >= len(m.commands)-10
		isAtBottom := m.cursor >= len(m.commands)-1
		
		if (isNearBottom || isAtBottom) && m.hasMore && !m.loading && len(m.commands) > 0 {
			fmt.Fprintf(os.Stderr, "DEBUG: Triggering load more commands! (cursor=%d, near=%t, at=%t)\n", 
				m.cursor, isNearBottom, isAtBottom)
			m.loading = true
			return m, tea.Batch(cmd, m.loadMoreCommands())
		}
		
	case key.Matches(msg, keys.PageUp):
		for i := 0; i < 10; i++ {
			m.table, _ = m.table.Update(tea.KeyMsg{Type: tea.KeyUp})
		}
		m.cursor = m.table.Cursor()
		
	case key.Matches(msg, keys.PageDown):
		for i := 0; i < 10; i++ {
			m.table, _ = m.table.Update(tea.KeyMsg{Type: tea.KeyDown})
		}
		m.cursor = m.table.Cursor()
		
		// Check if we need to load more data after page down
		if m.cursor >= len(m.commands)-10 && m.hasMore && !m.loading {
			m.loading = true
			return m, m.loadMoreCommands()
		}
		
	case key.Matches(msg, keys.Enter):
		if len(m.commands) > 0 {
			m.state = ViewDetail
		}
		
	case key.Matches(msg, keys.Search):
		m.state = ViewSearch
		m.searchInput.Focus()
		
	case key.Matches(msg, keys.Help):
		m.state = ViewHelp
		
	case key.Matches(msg, keys.Edit):
		if len(m.commands) > 0 {
			return m, m.editCommand(m.commands[m.cursor])
		}
		
	case key.Matches(msg, keys.Copy):
		if len(m.commands) > 0 {
			return m, m.copyCommand(m.commands[m.cursor])
		}
		
	case key.Matches(msg, keys.Delete):
		if len(m.commands) > 0 {
			m.state = ViewConfirm
			m.confirmAction = "delete"
			m.confirmTarget = m.commands[m.cursor].Command
		}
		
	case key.Matches(msg, keys.Execute):
		if len(m.commands) > 0 {
			m.state = ViewConfirm
			m.confirmAction = "execute"
			m.confirmTarget = m.commands[m.cursor].Command
		}
		
	case key.Matches(msg, keys.Fzf):
		return m, m.runFzf()
		
	case key.Matches(msg, keys.LoadMore):
		// Manual trigger for debugging infinite scroll
		if m.hasMore && !m.loading {
			fmt.Fprintf(os.Stderr, "DEBUG: Manual load more triggered!\n")
			m.loading = true
			return m, m.loadMoreCommands()
		} else {
			fmt.Fprintf(os.Stderr, "DEBUG: Manual load more - hasMore=%t, loading=%t\n", m.hasMore, m.loading)
		}
		
	case key.Matches(msg, keys.ToggleHost):
		m.showHost = !m.showHost
		m.updateTableSize()
		m.updateTableData()
		// Reset cursor to prevent index issues
		m.table.SetCursor(0)
		m.cursor = 0
		
	case key.Matches(msg, keys.ToggleShell):
		m.showShell = !m.showShell
		m.updateTableSize()
		m.updateTableData()
		// Reset cursor to prevent index issues
		m.table.SetCursor(0)
		m.cursor = 0
		
	case key.Matches(msg, keys.TogglePwd):
		m.showPwd = !m.showPwd
		m.updateTableSize()
		m.updateTableData()
		// Reset cursor to prevent index issues
		m.table.SetCursor(0)
		m.cursor = 0
	}
	
	return m, cmd
}

func (m Model) updateDetail(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Escape), key.Matches(msg, keys.Quit):
		m.state = ViewList
		
	case key.Matches(msg, keys.Edit):
		if len(m.commands) > 0 {
			return m, m.editCommand(m.commands[m.cursor])
		}
		
	case key.Matches(msg, keys.Copy):
		if len(m.commands) > 0 {
			return m, m.copyCommand(m.commands[m.cursor])
		}
		
	case key.Matches(msg, keys.Execute):
		if len(m.commands) > 0 {
			m.state = ViewConfirm
			m.confirmAction = "execute"
			m.confirmTarget = m.commands[m.cursor].Command
		}
		
	case key.Matches(msg, keys.Delete):
		if len(m.commands) > 0 {
			m.state = ViewConfirm
			m.confirmAction = "delete"
			m.confirmTarget = m.commands[m.cursor].Command
		}
	}
	
	return m, nil
}

func (m Model) updateSearch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	
	switch {
	case key.Matches(msg, keys.Escape):
		m.state = ViewList
		m.searchInput.Blur()
		
	case msg.Type == tea.KeyEnter:
		m.searchQuery = m.searchInput.Value()
		m.state = ViewList
		m.searchInput.Blur()
		// Trigger search by reloading commands
		return m, m.loadCommands(0, 50)
	}
	
	m.searchInput, cmd = m.searchInput.Update(msg)
	return m, cmd
}

func (m Model) updateHelp(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Escape), key.Matches(msg, keys.Help):
		m.state = ViewList
	}
	
	return m, nil
}

func (m Model) updateConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		m.state = ViewList
		if m.confirmAction == "delete" && len(m.commands) > 0 {
			return m, m.deleteCommand(m.commands[m.cursor])
		} else if m.confirmAction == "execute" && len(m.commands) > 0 {
			return m, m.executeCommand(m.commands[m.cursor])
		}
		
	case "n", "N", "esc":
		m.state = ViewList
	}
	
	return m, nil
}

func (m Model) View() string {
	switch m.state {
	case ViewList:
		return m.viewList()
	case ViewDetail:
		return m.viewDetail()
	case ViewSearch:
		return m.viewSearch()
	case ViewHelp:
		return m.viewHelp()
	case ViewConfirm:
		return m.viewConfirm()
	}
	
	return "Unknown view"
}

func (m Model) viewList() string {
	var s strings.Builder
	
	// Header - always visible
	title := "Total Recall Command History"
	filters := m.getFilterStatus()
	if filters != "" {
		title += " " + filterIndicatorStyle.Render("["+filters+"]")
	}
	
	s.WriteString(titleStyle.Render(title))
	s.WriteString("\n")
	
	// Search indicator
	if m.searchQuery != "" {
		s.WriteString(fmt.Sprintf("Search: %s\n", searchStyle.Render(m.searchQuery)))
	}
	
	// Loading indicator for pagination
	if m.loading {
		s.WriteString(warningStyle.Render("Loading more commands...") + "\n")
	}
	
	s.WriteString("\n")
	
	// Table view
	if len(m.commands) == 0 {
		s.WriteString(mutedStyle.Render("No commands found. Try adjusting your search query."))
	} else {
		s.WriteString(tableStyle.Render(m.table.View()))
	}
	
	// Footer - always visible
	s.WriteString("\n\n")
	
	footerText := fmt.Sprintf("Showing %d/%d commands", len(m.commands), m.totalCount)
	if m.loading {
		footerText += " • Loading more..."
	}
	if m.hasMore {
		footerText += " • Scroll down for more"
	}
	footerText += " • Press ? for help • q to quit"
	
	s.WriteString(helpStyle.Render(footerText))
	
	return s.String()
}

func (m Model) viewDetail() string {
	// Clear screen completely
	var s strings.Builder
	s.WriteString("\033[2J\033[H") // Clear screen and go to top
	
	if len(m.commands) == 0 {
		return "No command selected"
	}
	
	cmd := m.commands[m.cursor]
	
	s.WriteString(titleStyle.Render("Command Details"))
	s.WriteString("\n\n")
	
	// Command
	cmdStyle := successStyle
	if cmd.ReturnCode != 0 {
		cmdStyle = errorStyle
	}
	s.WriteString(detailStyle.Render(fmt.Sprintf("Command: %s", cmdStyle.Render(cmd.Command))))
	s.WriteString("\n\n")
	
	// Basic info
	s.WriteString(fmt.Sprintf("Directory: %s\n", cmd.Pwd))
	s.WriteString(fmt.Sprintf("Host: %s\n", cmd.Hostname))
	s.WriteString(fmt.Sprintf("Started: %s\n", cmd.StartTimestamp.Format("2006-01-02 15:04:05")))
	s.WriteString(fmt.Sprintf("Duration: %s\n", cmd.EndTimestamp.Sub(cmd.StartTimestamp).String()))
	
	returnCodeStyle := successStyle
	if cmd.ReturnCode != 0 {
		returnCodeStyle = errorStyle
	}
	s.WriteString(fmt.Sprintf("Return Code: %s\n", returnCodeStyle.Render(strconv.Itoa(cmd.ReturnCode))))
	
	if cmd.ShellPid != 0 {
		s.WriteString(fmt.Sprintf("Shell PID: %d\n", cmd.ShellPid))
	}
	
	// Environment variables (if any)
	if len(cmd.Environment) > 0 {
		s.WriteString("\nEnvironment Variables:\n")
		for key, value := range cmd.Environment {
			s.WriteString(fmt.Sprintf("  %s=%s\n", mutedStyle.Render(key), value))
		}
	}
	
	s.WriteString("\n")
	s.WriteString(helpStyle.Render("e: edit • c: copy • x: execute • d: delete • esc: back"))
	
	return s.String()
}

func (m Model) viewSearch() string {
	var s strings.Builder
	
	s.WriteString("\033[2J\033[H") // Clear screen
	s.WriteString(titleStyle.Render("Search Commands"))
	s.WriteString("\n\n")
	s.WriteString("Enter search query:\n")
	s.WriteString(searchStyle.Render(m.searchInput.View()))
	s.WriteString("\n\n")
	s.WriteString(helpStyle.Render("Press Enter to search, Esc to cancel"))
	
	return s.String()
}

func (m Model) viewHelp() string {
	var s strings.Builder
	
	s.WriteString("\033[2J\033[H") // Clear screen
	s.WriteString(titleStyle.Render("Total Recall Command History - Help"))
	s.WriteString("\n\n")
	
	// Show full help with all keybindings
	s.WriteString("Navigation:\n")
	s.WriteString("  j/k or ↑/↓     Move up/down\n")
	s.WriteString("  b/f           Page up/down\n")
	s.WriteString("  enter/l       View details\n")
	s.WriteString("\n")
	s.WriteString("Actions:\n")
	s.WriteString("  e             Edit command in vim\n")
	s.WriteString("  c             Copy command to clipboard\n")
	s.WriteString("  x             Execute command\n")
	s.WriteString("  d             Delete command\n")
	s.WriteString("\n")
	s.WriteString("Search & Filter:\n")
	s.WriteString("  /             Search commands\n")
	s.WriteString("  ctrl+f        Fuzzy find (if fzf available)\n")
	s.WriteString("  h             Toggle host column\n")
	s.WriteString("  s             Toggle shell PID column\n")
	s.WriteString("  p             Toggle PWD column\n")
	s.WriteString("\n")
	s.WriteString("Other:\n")
	s.WriteString("  ?             This help\n")
	s.WriteString("  m             Load more commands (debug)\n")
	s.WriteString("  esc           Back/Cancel\n")
	s.WriteString("  q             Quit\n")
	
	s.WriteString("\n\n")
	s.WriteString(helpStyle.Render("Press ? or Esc to close help"))
	
	return s.String()
}

func (m Model) viewConfirm() string {
	var s strings.Builder
	
	s.WriteString("\033[2J\033[H") // Clear screen
	
	action := "Action"
	if m.confirmAction == "delete" {
		action = errorStyle.Render("DELETE")
	} else if m.confirmAction == "execute" {
		action = warningStyle.Render("EXECUTE")
	}
	
	s.WriteString(titleStyle.Render(fmt.Sprintf("Confirm %s", action)))
	s.WriteString("\n\n")
	
	// Truncate long commands for confirmation
	target := m.confirmTarget
	if len(target) > 60 {
		target = target[:57] + "..."
	}
	
	s.WriteString(fmt.Sprintf("Are you sure you want to %s this command?\n\n", strings.ToLower(m.confirmAction)))
	s.WriteString(focusedStyle.Render(target))
	s.WriteString("\n\n")
	s.WriteString(helpStyle.Render("Press Y to confirm, N or Esc to cancel"))
	
	return s.String()
}

func (m Model) getFilterStatus() string {
	var filters []string
	
	if m.showHost {
		filters = append(filters, "H")
	}
	if m.showShell {
		filters = append(filters, "S")
	}
	if m.showPwd {
		filters = append(filters, "P")
	}
	
	return strings.Join(filters, "")
}

// Commands for interacting with the system

func (m Model) loadCommands(offset, limit int) tea.Cmd {
	return func() tea.Msg {
		query := m.buildElasticsearchQuery(offset, limit)
		
		// Query elasticsearch via socket
		if response, err := m.queryElasticsearch(query); err == nil {
			return commandsLoadedMsg{
				commands: response.Commands,
				total:    response.Total,
				hasMore:  response.HasMore,
				append:   false, // Replace existing commands
			}
		} else {
			return errorMsg{err}
		}
	}
}

func (m Model) loadMoreCommands() tea.Cmd {
	return func() tea.Msg {
		offset := len(m.commands)
		limit := 50
		query := m.buildElasticsearchQuery(offset, limit)
		
		// Query elasticsearch via socket
		if response, err := m.queryElasticsearch(query); err == nil {
			return commandsLoadedMsg{
				commands: response.Commands,
				total:    response.Total,
				hasMore:  response.HasMore,
				append:   true, // Append to existing commands
			}
		} else {
			return errorMsg{err}
		}
	}
}

func (m Model) buildElasticsearchQuery(offset, limit int) map[string]interface{} {
	query := map[string]interface{}{
		"from": offset,
		"size": limit,
		"sort": []map[string]interface{}{
			{"start_timestamp": map[string]interface{}{"order": "desc"}},
		},
	}
	
	// Build filter conditions
	var filters []map[string]interface{}
	
	// Always filter by current host and pwd (this is the main purpose)
	filters = append(filters, map[string]interface{}{
		"term": map[string]interface{}{
			"hostname.keyword": m.currentHost,
		},
	})
	
	filters = append(filters, map[string]interface{}{
		"term": map[string]interface{}{
			"pwd.keyword": m.currentPwd,
		},
	})
	
	// Search query
	var must []map[string]interface{}
	if m.searchQuery != "" {
		must = append(must, map[string]interface{}{
			"multi_match": map[string]interface{}{
				"query":  m.searchQuery,
				"fields": []string{"command^2", "pwd"},
			},
		})
	}
	
	// Combine filters
	boolQuery := map[string]interface{}{}
	if len(filters) > 0 {
		boolQuery["filter"] = filters
	}
	if len(must) > 0 {
		boolQuery["must"] = must
	}
	
	if len(boolQuery) > 0 {
		query["query"] = map[string]interface{}{
			"bool": boolQuery,
		}
	}
	
	return query
}

func (m Model) queryElasticsearch(query map[string]interface{}) (*QueryResponse, error) {
	// Connect to proxy and make elasticsearch query
	conn, err := net.Dial("unix", m.socketPath)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	
	request := map[string]interface{}{
		"action": "search_history",
		"query":  query,
	}
	
	data, _ := json.Marshal(request)
	conn.Write(append(data, '\n'))
	
	// Read response
	var response QueryResponse
	decoder := json.NewDecoder(conn)
	if err := decoder.Decode(&response); err != nil {
		return nil, err
	}
	
	return &response, nil
}

func (m Model) editCommand(cmd Command) tea.Cmd {
	// Create temporary file
	tmpFile, err := os.CreateTemp("", "totalrecall-edit-*.sh")
	if err != nil {
		return func() tea.Msg { return errorMsg{err} }
	}
	
	// Write command to file
	tmpFile.WriteString(cmd.Command)
	tmpFile.Close()
	
	// Create vim command
	vimCmd := exec.Command("vim", tmpFile.Name())
	
	// Use tea.ExecProcess to run vim
	return tea.ExecProcess(vimCmd, func(err error) tea.Msg {
		defer os.Remove(tmpFile.Name())
		
		if err != nil {
			return errorMsg{err}
		}
		
		// Read edited content
		content, readErr := os.ReadFile(tmpFile.Name())
		if readErr != nil {
			return errorMsg{readErr}
		}
		
		// Put edited command on clipboard
		editedCmd := strings.TrimSpace(string(content))
		if clipErr := setClipboard(editedCmd); clipErr != nil {
			return errorMsg{clipErr}
		}
		
		return vimFinishedMsg{}
	})
}

func (m Model) copyCommand(cmd Command) tea.Cmd {
	return func() tea.Msg {
		if err := setClipboard(cmd.Command); err != nil {
			return errorMsg{err}
		}
		return nil
	}
}

func (m Model) deleteCommand(cmd Command) tea.Cmd {
	return func() tea.Msg {
		// Delete from elasticsearch via proxy
		conn, err := net.Dial("unix", m.socketPath)
		if err != nil {
			return errorMsg{err}
		}
		defer conn.Close()
		
		request := map[string]interface{}{
			"action":     "delete_command",
			"command_id": cmd.ID,
		}
		
		data, _ := json.Marshal(request)
		conn.Write(append(data, '\n'))
		
		return commandDeletedMsg{id: cmd.ID}
	}
}

func (m Model) executeCommand(cmd Command) tea.Cmd {
	// Build execution script
	var script strings.Builder
	
	// Change to the original directory
	script.WriteString(fmt.Sprintf("cd %s\n", cmd.Pwd))
	
	// Set environment variables
	for key, value := range cmd.Environment {
		// Skip internal variables and hashed values
		if strings.HasPrefix(key, "___") || strings.HasPrefix(value, "h8_") {
			continue
		}
		script.WriteString(fmt.Sprintf("export %s=%s\n", key, value))
	}
	
	// Add the command
	script.WriteString(cmd.Command)
	script.WriteString("\n")
	
	// Create temporary script file
	tmpFile, err := os.CreateTemp("", "totalrecall-exec-*.sh")
	if err != nil {
		return func() tea.Msg { return errorMsg{err} }
	}
	
	tmpFile.WriteString(script.String())
	tmpFile.Close()
	
	// Make executable
	os.Chmod(tmpFile.Name(), 0755)
	
	// Execute in a new shell
	execCmd := exec.Command("bash", tmpFile.Name())
	
	return tea.ExecProcess(execCmd, func(err error) tea.Msg {
		defer os.Remove(tmpFile.Name())
		
		if err != nil {
			return errorMsg{err}
		}
		
		return vimFinishedMsg{}
	})
}

func (m Model) runFzf() tea.Cmd {
	// Check if fzf is available
	if _, err := exec.LookPath("fzf"); err != nil {
		return func() tea.Msg { return errorMsg{fmt.Errorf("fzf not found in PATH")} }
	}
	
	// Create input for fzf
	var lines []string
	for i, cmd := range m.commands {
		timeStr := cmd.StartTimestamp.Format("15:04:05")
		line := fmt.Sprintf("%d: %s %s", i, timeStr, cmd.Command)
		lines = append(lines, line)
	}
	
	if len(lines) == 0 {
		return func() tea.Msg { return errorMsg{fmt.Errorf("no commands available")} }
	}
	
	// Create a temporary file with the lines for fzf
	tmpFile, err := os.CreateTemp("", "totalrecall-fzf-*.txt")
	if err != nil {
		return func() tea.Msg { return errorMsg{err} }
	}
	
	tmpFile.WriteString(strings.Join(lines, "\n"))
	tmpFile.Close()
	
	// Run fzf
	fzfCmd := exec.Command("fzf", "--reverse", "--height=50%")
	fzfCmd.Stdin, _ = os.Open(tmpFile.Name())
	
	return tea.ExecProcess(fzfCmd, func(err error) tea.Msg {
		defer os.Remove(tmpFile.Name())
		
		if err != nil {
			return errorMsg{err}
		}
		
		return vimFinishedMsg{}
	})
}

func setClipboard(text string) error {
	// Try different clipboard commands
	commands := [][]string{
		{"pbcopy"},                              // macOS
		{"xclip", "-selection", "clipboard"},    // Linux
		{"wl-copy"},                             // Wayland
	}
	
	for _, cmdArgs := range commands {
		if _, err := exec.LookPath(cmdArgs[0]); err == nil {
			cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
			cmd.Stdin = strings.NewReader(text)
			if err := cmd.Run(); err == nil {
				return nil
			}
		}
	}
	
	return fmt.Errorf("no clipboard command available")
}

// TUIRequest represents a request from the TUI
type TUIRequest struct {
	Action    string                 `json:"action"`
	Query     map[string]interface{} `json:"query,omitempty"`
	CommandID string                 `json:"command_id,omitempty"`
}

// Utility functions
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func main() {
	socketPath := "/tmp/totalrecall-proxy.sock"
	
	// Parse command line arguments
	if len(os.Args) > 1 {
		socketPath = os.Args[1]
	}
	
	model := initialModel(socketPath)
	
	p := tea.NewProgram(model, tea.WithAltScreen())
	
	if _, err := p.Run(); err != nil {
		log.Fatal(err)
	}
}
