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
	Quit       key.Binding
	Escape     key.Binding
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Help, k.Quit}
}

func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.PageUp, k.PageDown},
		{k.Enter, k.Edit, k.Execute, k.Copy},
		{k.Search, k.Fzf, k.Delete},
		{k.ToggleHost, k.ToggleShell, k.TogglePwd},
		{k.Help, k.Quit},
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
		key.WithHelp("b/ctrl+↑", "page up"),
	),
	PageDown: key.NewBinding(
		key.WithKeys("f", "ctrl+down"),
		key.WithHelp("f/ctrl+↓", "page down"),
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
		key.WithKeys("f"),
		key.WithHelp("f", "fuzzy find"),
	),
	ToggleHost: key.NewBinding(
		key.WithKeys("h"),
		key.WithHelp("h", "toggle host filter"),
	),
	ToggleShell: key.NewBinding(
		key.WithKeys("s"),
		key.WithHelp("s", "toggle shell filter"),
	),
	TogglePwd: key.NewBinding(
		key.WithKeys("p"),
		key.WithHelp("p", "toggle pwd filter"),
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
	
	// Filters
	searchQuery string
	hostFilter  bool
	shellFilter bool
	pwdFilter   bool
	
	// Current context
	currentPwd  string
	currentHost string
	parentPid   int
	
	// UI Components
	searchInput textinput.Model
	viewport    viewport.Model
	help        help.Model
	
	// Confirmation state
	confirmAction string
	confirmTarget string
	
	// Socket connection
	socketPath string
	
	// Loading state
	loading bool
	
	// Cache for fast loading
	useCache bool
}

// Messages
type commandsLoadedMsg struct {
	commands []Command
	total    int
	hasMore  bool
}

type commandDeletedMsg struct {
	id string
}

type errorMsg struct {
	err error
}

// Initialize the model
func initialModel(socketPath string, useCache bool) Model {
	ti := textinput.New()
	ti.Placeholder = "Search commands..."
	ti.CharLimit = 100

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
		viewport:    vp,
		help:        help.New(),
		currentPwd:  pwd,
		currentHost: hostname,
		parentPid:   parentPid,
		socketPath:  socketPath,
		useCache:    useCache,
		// Default filters
		hostFilter: true,  // Show only current host by default
		pwdFilter:  true,  // Show only current directory by default
		shellFilter: false, // Show all shells by default
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
		m.viewport.Width = msg.Width
		m.viewport.Height = msg.Height - 6 // Reserve space for header/footer
		
	case commandsLoadedMsg:
		m.commands = msg.commands
		m.totalCount = msg.total
		m.hasMore = msg.hasMore
		m.loading = false
		if len(m.commands) > 0 && m.cursor >= len(m.commands) {
			m.cursor = len(m.commands) - 1
		}
		
	case commandDeletedMsg:
		// Remove deleted command from list
		for i, cmd := range m.commands {
			if cmd.ID == msg.id {
				m.commands = append(m.commands[:i], m.commands[i+1:]...)
				if m.cursor >= len(m.commands) && len(m.commands) > 0 {
					m.cursor = len(m.commands) - 1
				}
				break
			}
		}
		
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

func (m Model) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Quit):
		return m, tea.Quit
		
	case key.Matches(msg, keys.Up):
		if m.cursor > 0 {
			m.cursor--
		}
		
	case key.Matches(msg, keys.Down):
		if m.cursor < len(m.commands)-1 {
			m.cursor++
		} else if m.hasMore {
			// Load more commands
			return m, m.loadCommands(len(m.commands), 50)
		}
		
	case key.Matches(msg, keys.PageUp):
		m.cursor = max(0, m.cursor-10)
		
	case key.Matches(msg, keys.PageDown):
		m.cursor = min(len(m.commands)-1, m.cursor+10)
		if m.cursor == len(m.commands)-1 && m.hasMore {
			return m, m.loadCommands(len(m.commands), 50)
		}
		
	case key.Matches(msg, keys.Enter):
		m.state = ViewDetail
		
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
		
	case key.Matches(msg, keys.ToggleHost):
		m.hostFilter = !m.hostFilter
		return m, m.loadCommands(0, 50)
		
	case key.Matches(msg, keys.ToggleShell):
		m.shellFilter = !m.shellFilter
		return m, m.loadCommands(0, 50)
		
	case key.Matches(msg, keys.TogglePwd):
		m.pwdFilter = !m.pwdFilter
		return m, m.loadCommands(0, 50)
	}
	
	return m, nil
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
	
	// Header
	title := "Total Recall Command History"
	filters := m.getFilterStatus()
	if filters != "" {
		title += " " + filterIndicatorStyle.Render("["+filters+"]")
	}
	
	s.WriteString(titleStyle.Render(title))
	s.WriteString("\n\n")
	
	// Search bar
	if m.searchQuery != "" {
		s.WriteString(fmt.Sprintf("Search: %s\n", searchStyle.Render(m.searchQuery)))
	}
	
	// Commands list
	if len(m.commands) == 0 {
		s.WriteString(mutedStyle.Render("No commands found. Try adjusting your filters or search query."))
	} else {
		for i, cmd := range m.commands {
			line := m.formatCommandLine(cmd, i == m.cursor)
			s.WriteString(line)
			s.WriteString("\n")
		}
	}
	
	// Footer
	s.WriteString("\n")
	s.WriteString(helpStyle.Render(fmt.Sprintf(
		"Showing %d/%d commands • Press ? for help • q to quit",
		len(m.commands), m.totalCount,
	)))
	
	return s.String()
}

func (m Model) viewDetail() string {
	if len(m.commands) == 0 {
		return "No command selected"
	}
	
	cmd := m.commands[m.cursor]
	var s strings.Builder
	
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
	
	s.WriteString(titleStyle.Render("Search Commands"))
	s.WriteString("\n\n")
	s.WriteString("Enter search query:\n")
	s.WriteString(m.searchInput.View())
	s.WriteString("\n\n")
	s.WriteString(helpStyle.Render("Press Enter to search, Esc to cancel"))
	
	return s.String()
}

func (m Model) viewHelp() string {
	var s strings.Builder
	
	s.WriteString(titleStyle.Render("Total Recall Command History - Help"))
	s.WriteString("\n\n")
	
	s.WriteString(m.help.View(keys))
	
	s.WriteString("\n\n")
	s.WriteString(helpStyle.Render("Press ? or Esc to close help"))
	
	return s.String()
}

func (m Model) viewConfirm() string {
	var s strings.Builder
	
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

func (m Model) formatCommandLine(cmd Command, focused bool) string {
	// Format timestamp
	timeStr := cmd.StartTimestamp.Format("15:04:05")
	
	// Format command (truncate if too long)
	cmdStr := cmd.Command
	maxCmdLen := 80
	if len(cmdStr) > maxCmdLen {
		cmdStr = cmdStr[:maxCmdLen-3] + "..."
	}
	
	// Choose style based on return code
	var cmdStyle lipgloss.Style
	switch cmd.ReturnCode {
	case 0:
		cmdStyle = successStyle
	case 1:
		cmdStyle = errorStyle
	case 2:
		cmdStyle = warningStyle
	default:
		if cmd.ReturnCode > 128 {
			cmdStyle = errorStyle.Copy().Foreground(lipgloss.Color("#DC2626")) // Darker red for signals
		} else {
			cmdStyle = mutedStyle
		}
	}
	
	// Format the line
	line := fmt.Sprintf("%s %s", 
		mutedStyle.Render(timeStr),
		cmdStyle.Render(cmdStr),
	)
	
	// Add focus highlight
	if focused {
		line = focusedStyle.Render("► " + line)
	} else {
		line = "  " + line
	}
	
	return line
}

func (m Model) getFilterStatus() string {
	var filters []string
	
	if m.hostFilter {
		filters = append(filters, "H")
	}
	if m.shellFilter {
		filters = append(filters, "S")
	}
	if m.pwdFilter {
		filters = append(filters, "P")
	}
	
	return strings.Join(filters, "")
}

// Commands for interacting with the system

func (m Model) loadCommands(offset, limit int) tea.Cmd {
	return func() tea.Msg {
		query := m.buildElasticsearchQuery(offset, limit)
		
		// Try cache first if this is the initial load
		if offset == 0 && m.useCache {
			if response, err := m.queryCache(query); err == nil {
				return commandsLoadedMsg{
					commands: response.Commands,
					total:    response.Total,
					hasMore:  response.HasMore,
				}
			}
		}
		
		// Query elasticsearch via socket
		if response, err := m.queryElasticsearch(query); err == nil {
			return commandsLoadedMsg{
				commands: response.Commands,
				total:    response.Total,
				hasMore:  response.HasMore,
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
	
	// Host filter
	if m.hostFilter {
		filters = append(filters, map[string]interface{}{
			"term": map[string]interface{}{
				"hostname.keyword": m.currentHost,
			},
		})
	}
	
	// PWD filter
	if m.pwdFilter {
		filters = append(filters, map[string]interface{}{
			"term": map[string]interface{}{
				"pwd.keyword": m.currentPwd,
			},
		})
	}
	
	// Shell filter
	if m.shellFilter {
		filters = append(filters, map[string]interface{}{
			"term": map[string]interface{}{
				"shellpid": m.parentPid,
			},
		})
	}
	
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

func (m Model) queryCache(query map[string]interface{}) (*QueryResponse, error) {
	// Connect to proxy and request cached results
	conn, err := net.Dial("unix", m.socketPath)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	
	request := map[string]interface{}{
		"action": "get_cache",
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
	return func() tea.Msg {
		// Create temporary file
		tmpFile, err := os.CreateTemp("", "totalrecall-edit-*.sh")
		if err != nil {
			return errorMsg{err}
		}
		defer os.Remove(tmpFile.Name())
		
		// Write command to file
		tmpFile.WriteString(cmd.Command)
		tmpFile.Close()
		
		// Open in vim
		vimCmd := exec.Command("vim", tmpFile.Name())
		vimCmd.Stdin = os.Stdin
		vimCmd.Stdout = os.Stdout
		vimCmd.Stderr = os.Stderr
		
		if err := vimCmd.Run(); err != nil {
			return errorMsg{err}
		}
		
		// Read edited content
		content, err := os.ReadFile(tmpFile.Name())
		if err != nil {
			return errorMsg{err}
		}
		
		// Put edited command on clipboard and prepare for execution
		editedCmd := strings.TrimSpace(string(content))
		if err := m.setClipboard(editedCmd); err != nil {
			return errorMsg{err}
		}
		
		return nil // Command is now in clipboard
	}
}

func (m Model) copyCommand(cmd Command) tea.Cmd {
	return func() tea.Msg {
		if err := m.setClipboard(cmd.Command); err != nil {
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
	return func() tea.Msg {
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
			return errorMsg{err}
		}
		defer os.Remove(tmpFile.Name())
		
		tmpFile.WriteString(script.String())
		tmpFile.Close()
		
		// Make executable
		os.Chmod(tmpFile.Name(), 0755)
		
		// Execute in a new shell
		execCmd := exec.Command("bash", tmpFile.Name())
		execCmd.Stdin = os.Stdin
		execCmd.Stdout = os.Stdout
		execCmd.Stderr = os.Stderr
		
		if err := execCmd.Run(); err != nil {
			return errorMsg{err}
		}
		
		return nil
	}
}

func (m Model) runFzf() tea.Cmd {
	return func() tea.Msg {
		// Check if fzf is available
		if _, err := exec.LookPath("fzf"); err != nil {
			return errorMsg{fmt.Errorf("fzf not found in PATH")}
		}
		
		// Create input for fzf
		var lines []string
		for i, cmd := range m.commands {
			timeStr := cmd.StartTimestamp.Format("15:04:05")
			line := fmt.Sprintf("%d: %s %s", i, timeStr, cmd.Command)
			lines = append(lines, line)
		}
		
		if len(lines) == 0 {
			return errorMsg{fmt.Errorf("no commands available")}
		}
		
		// Run fzf
		fzfCmd := exec.Command("fzf", "--reverse", "--height=50%")
		fzfCmd.Stdin = strings.NewReader(strings.Join(lines, "\n"))
		fzfCmd.Stderr = os.Stderr
		
		output, err := fzfCmd.Output()
		if err != nil {
			return errorMsg{err}
		}
		
		// Parse selection
		selection := strings.TrimSpace(string(output))
		if selection == "" {
			return nil
		}
		
		// Extract index
		parts := strings.SplitN(selection, ":", 2)
		if len(parts) < 2 {
			return nil
		}
		
		index, err := strconv.Atoi(parts[0])
		if err != nil || index < 0 || index >= len(m.commands) {
			return nil
		}
		
		// This would need to be handled by returning a special message
		// For now, we'll just return nil
		return nil
	}
}

func (m Model) setClipboard(text string) error {
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

func main() {
	socketPath := "/tmp/totalrecall-proxy.sock"
	useCache := true
	
	// Parse command line arguments
	if len(os.Args) > 1 {
		socketPath = os.Args[1]
	}
	
	model := initialModel(socketPath, useCache)
	
	p := tea.NewProgram(model, tea.WithAltScreen())
	
	if _, err := p.Run(); err != nil {
		log.Fatal(err)
	}
}
