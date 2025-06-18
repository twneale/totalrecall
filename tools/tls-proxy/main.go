package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

var debugMode bool

func debugLog(format string, args ...interface{}) {
	if debugMode {
		log.Printf("[DEBUG] "+format, args...)
	}
}

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

// QueryResponse represents elasticsearch response for TUI
type QueryResponse struct {
	Commands []Command `json:"commands"`
	Total    int       `json:"total"`
	HasMore  bool      `json:"has_more"`
}

// TUIRequest represents a request from the TUI
type TUIRequest struct {
	Action    string                 `json:"action"`
	Query     map[string]interface{} `json:"query,omitempty"`
	CommandID string                 `json:"command_id,omitempty"`
}

// CommandCache stores prefetched command history for fast TUI loading
type CommandCache struct {
	mutex    sync.RWMutex
	entries  map[string]*CacheEntry
	maxAge   time.Duration
	maxSize  int
}

type CacheEntry struct {
	response  *QueryResponse
	timestamp time.Time
	queryHash string
}

func NewCommandCache(maxAge time.Duration, maxSize int) *CommandCache {
	cache := &CommandCache{
		entries: make(map[string]*CacheEntry),
		maxAge:  maxAge,
		maxSize: maxSize,
	}
	
	// Start cleanup goroutine
	go cache.cleanup()
	
	return cache
}

func (c *CommandCache) cleanup() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	
	for range ticker.C {
		c.mutex.Lock()
		now := time.Now()
		for key, entry := range c.entries {
			if now.Sub(entry.timestamp) > c.maxAge {
				delete(c.entries, key)
				debugLog("Expired cache entry: %s", key)
			}
		}
		c.mutex.Unlock()
	}
}

func (c *CommandCache) Get(queryHash string) (*QueryResponse, bool) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	
	entry, exists := c.entries[queryHash]
	if !exists {
		return nil, false
	}
	
	if time.Since(entry.timestamp) > c.maxAge {
		return nil, false
	}
	
	return entry.response, true
}

func (c *CommandCache) Set(queryHash string, response *QueryResponse) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	
	// Evict old entries if cache is full
	if len(c.entries) >= c.maxSize {
		// Simple LRU: remove oldest entry
		var oldestKey string
		var oldestTime time.Time
		for key, entry := range c.entries {
			if oldestKey == "" || entry.timestamp.Before(oldestTime) {
				oldestKey = key
				oldestTime = entry.timestamp
			}
		}
		if oldestKey != "" {
			delete(c.entries, oldestKey)
		}
	}
	
	c.entries[queryHash] = &CacheEntry{
		response:  response,
		timestamp: time.Now(),
		queryHash: queryHash,
	}
	
	debugLog("Cached query result: %s (%d commands)", queryHash, len(response.Commands))
}

func (c *CommandCache) Invalidate() {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	
	// Clear all cached entries when new commands arrive
	c.entries = make(map[string]*CacheEntry)
	debugLog("Invalidated command cache")
}

// Subscriber struct
type Subscriber struct {
	id     string
	conn   net.Conn
	writer *bufio.Writer
	filter map[string]string
}

// PubSubHub struct
type PubSubHub struct {
	subscribers map[string]*Subscriber
	subMutex    sync.RWMutex
	totalEvents int64
	totalSubs   int64
}

func NewPubSubHub() *PubSubHub {
	return &PubSubHub{
		subscribers: make(map[string]*Subscriber),
	}
}

func (hub *PubSubHub) Subscribe(id string, conn net.Conn, filter map[string]string) {
	hub.subMutex.Lock()
	defer hub.subMutex.Unlock()

	if existing, exists := hub.subscribers[id]; exists {
		existing.conn.Close()
	}

	subscriber := &Subscriber{
		id:     id,
		conn:   conn,
		writer: bufio.NewWriter(conn),
		filter: filter,
	}

	hub.subscribers[id] = subscriber
	hub.totalSubs++

	debugLog("New subscriber: %s (total: %d)", id, len(hub.subscribers))
}

func (hub *PubSubHub) Unsubscribe(id string) {
	hub.subMutex.Lock()
	defer hub.subMutex.Unlock()

	if subscriber, exists := hub.subscribers[id]; exists {
		subscriber.conn.Close()
		delete(hub.subscribers, id)
		debugLog("Subscriber disconnected: %s (remaining: %d)", id, len(hub.subscribers))
	}
}

func (hub *PubSubHub) Publish(eventData []byte) {
	hub.subMutex.RLock()
	defer hub.subMutex.RUnlock()

	if len(hub.subscribers) == 0 {
		debugLog("No subscribers to publish to")
		return
	}

	debugLog("Publishing to %d subscribers", len(hub.subscribers))

	var event map[string]interface{}
	json.Unmarshal(eventData, &event)

	deadSubs := []string{}

	for id, subscriber := range hub.subscribers {
		if !hub.matchesFilter(event, subscriber.filter) {
			continue
		}

		subscriber.conn.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))
		
		_, err := subscriber.writer.Write(append(eventData, '\n'))
		if err == nil {
			err = subscriber.writer.Flush()
		}

		subscriber.conn.SetWriteDeadline(time.Time{})

		if err != nil {
			debugLog("Failed to send to subscriber %s: %v", id, err)
			deadSubs = append(deadSubs, id)
		} else {
			debugLog("Successfully sent to subscriber %s", id)
		}
	}

	for _, id := range deadSubs {
		hub.Unsubscribe(id)
	}

	hub.totalEvents++
}

func (hub *PubSubHub) matchesFilter(event map[string]interface{}, filter map[string]string) bool {
	if len(filter) == 0 {
		return true
	}

	for key, expectedValue := range filter {
		if actualValue, exists := event[key]; !exists {
			return false
		} else if actualValueStr := fmt.Sprintf("%v", actualValue); actualValueStr != expectedValue {
			return false
		}
	}

	return true
}

func (hub *PubSubHub) GetStats() (int, int64, int64) {
	hub.subMutex.RLock()
	defer hub.subMutex.RUnlock()
	return len(hub.subscribers), hub.totalEvents, hub.totalSubs
}

// ConnectionPool struct
type ConnectionPool struct {
	connections chan *tls.Conn
	targetAddr  string
	tlsConfig   *tls.Config
	poolSize    int
	activeConns int
	totalSent   int64
	totalErrors int64
	mutex       sync.RWMutex
}

func NewConnectionPool(targetAddr string, tlsConfig *tls.Config, poolSize int) *ConnectionPool {
	return &ConnectionPool{
		connections: make(chan *tls.Conn, poolSize),
		targetAddr:  targetAddr,
		tlsConfig:   tlsConfig,
		poolSize:    poolSize,
	}
}

func (pool *ConnectionPool) getConnection() (*tls.Conn, error) {
	select {
	case conn := <-pool.connections:
		conn.SetDeadline(time.Now().Add(100 * time.Millisecond))
		_, err := conn.Write([]byte{})
		conn.SetDeadline(time.Time{})
		
		if err != nil {
			conn.Close()
			pool.mutex.Lock()
			pool.activeConns--
			pool.mutex.Unlock()
		} else {
			return conn, nil
		}
	default:
	}

	dialer := &net.Dialer{Timeout: 3 * time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", pool.targetAddr, pool.tlsConfig)
	if err != nil {
		pool.mutex.Lock()
		pool.totalErrors++
		pool.mutex.Unlock()
		return nil, fmt.Errorf("failed to create TLS connection to %s: %v", pool.targetAddr, err)
	}

	pool.mutex.Lock()
	pool.activeConns++
	pool.mutex.Unlock()

	debugLog("Created new TLS connection to %s (active: %d)", pool.targetAddr, pool.activeConns)
	return conn, nil
}

func (pool *ConnectionPool) returnConnection(conn *tls.Conn) {
	select {
	case pool.connections <- conn:
		debugLog("Returned connection to %s pool", pool.targetAddr)
	default:
		conn.Close()
		pool.mutex.Lock()
		pool.activeConns--
		pool.mutex.Unlock()
		debugLog("Pool full for %s, closed connection (active: %d)", pool.targetAddr, pool.activeConns)
	}
}

func (pool *ConnectionPool) GetStats() (int, int, int64, int64) {
	pool.mutex.RLock()
	defer pool.mutex.RUnlock()
	return pool.activeConns, len(pool.connections), pool.totalSent, pool.totalErrors
}

// Enhanced TLS Proxy with command history caching
type EnhancedTLSProxyWithCache struct {
	socketPath     string
	fluentbitPool  *ConnectionPool
	esHTTPClient   *http.Client
	esBaseURL      string
	pubsub         *PubSubHub
	listener       net.Listener
	commandCache   *CommandCache
}

func NewEnhancedTLSProxyWithCache(socketPath string, 
	fluentbitAddr, esAddr string,
	fluentbitTLS, esTLS *tls.Config,
	poolSize int) *EnhancedTLSProxyWithCache {
	
	esHTTPClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: esTLS,
		},
		Timeout: 30 * time.Second,
	}
	
	return &EnhancedTLSProxyWithCache{
		socketPath:    socketPath,
		fluentbitPool: NewConnectionPool(fluentbitAddr, fluentbitTLS, poolSize),
		esHTTPClient:  esHTTPClient,
		esBaseURL:     fmt.Sprintf("https://%s", esAddr),
		pubsub:        NewPubSubHub(),
		commandCache:  NewCommandCache(5*time.Minute, 100), // 5 min cache, max 100 entries
	}
}

func (p *EnhancedTLSProxyWithCache) handleClient(clientConn net.Conn) {
	defer clientConn.Close()

	reader := bufio.NewReader(clientConn)
	
	firstLine, err := reader.ReadString('\n')
	if err != nil {
		debugLog("Failed to read first line: %v", err)
		return
	}

	firstLine = strings.TrimSpace(firstLine)
	debugLog("Received first line: %s", firstLine)

	// Check if this is a TUI request (JSON with "action" field)
	if p.isTUIRequest(firstLine) {
		debugLog("Handling as TUI request")
		p.handleTUIRequest(clientConn, firstLine)
		return
	}

	// Fall back to original proxy behavior
	switch {
	case isHTTPRequest(firstLine):
		debugLog("Handling as HTTP request for Elasticsearch")
		p.handleESRequest(clientConn, reader, firstLine)
		
	case strings.HasPrefix(firstLine, "SUBSCRIBE"):
		debugLog("Handling as pub/sub subscription")
		parts := strings.Fields(firstLine)
		subscriberID := "anonymous"
		filterStr := ""
		
		if len(parts) >= 2 {
			subscriberID = parts[1]
		}
		if len(parts) >= 3 {
			filterStr = strings.Join(parts[2:], " ")
		}
		
		p.handleSubscriber(clientConn, subscriberID, filterStr)
		
	default:
		debugLog("Handling as fluent-bit JSON event")
		if err := p.processFluentbitEventWithCache([]byte(firstLine)); err != nil {
			debugLog("Failed to process fluent-bit event: %v", err)
		}
		
		scanner := bufio.NewScanner(reader)
		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}
			
			if err := p.processFluentbitEventWithCache(line); err != nil {
				debugLog("Failed to process fluent-bit event: %v", err)
			}
		}
	}
}

func (p *EnhancedTLSProxyWithCache) isTUIRequest(line string) bool {
	var req TUIRequest
	return json.Unmarshal([]byte(line), &req) == nil && req.Action != ""
}

func (p *EnhancedTLSProxyWithCache) handleTUIRequest(clientConn net.Conn, requestLine string) {
	var req TUIRequest
	if err := json.Unmarshal([]byte(requestLine), &req); err != nil {
		p.sendTUIError(clientConn, fmt.Errorf("invalid TUI request: %v", err))
		return
	}
	
	debugLog("TUI request: %s", req.Action)
	
	switch req.Action {
	case "get_cache":
		p.handleGetCache(clientConn, req)
	case "search_history":
		p.handleSearchHistory(clientConn, req)
	case "delete_command":
		p.handleDeleteCommand(clientConn, req)
	default:
		p.sendTUIError(clientConn, fmt.Errorf("unknown action: %s", req.Action))
	}
}

func (p *EnhancedTLSProxyWithCache) handleGetCache(clientConn net.Conn, req TUIRequest) {
	queryHash := p.hashQuery(req.Query)
	
	if cached, found := p.commandCache.Get(queryHash); found {
		debugLog("Cache hit for query: %s", queryHash)
		p.sendTUIResponse(clientConn, cached)
		return
	}
	
	debugLog("Cache miss for query: %s", queryHash)
	// Fall back to live query
	p.handleSearchHistory(clientConn, req)
}

func (p *EnhancedTLSProxyWithCache) handleSearchHistory(clientConn net.Conn, req TUIRequest) {
	response, err := p.queryElasticsearchForCommands(req.Query)
	if err != nil {
		p.sendTUIError(clientConn, err)
		return
	}
	
	// Cache the result
	queryHash := p.hashQuery(req.Query)
	p.commandCache.Set(queryHash, response)
	
	p.sendTUIResponse(clientConn, response)
}

func (p *EnhancedTLSProxyWithCache) handleDeleteCommand(clientConn net.Conn, req TUIRequest) {
	if req.CommandID == "" {
		p.sendTUIError(clientConn, fmt.Errorf("command_id required"))
		return
	}
	
	// Delete from elasticsearch
	deleteURL := fmt.Sprintf("%s/totalrecall*/_doc/%s", p.esBaseURL, req.CommandID)
	
	deleteReq, err := http.NewRequest("DELETE", deleteURL, nil)
	if err != nil {
		p.sendTUIError(clientConn, err)
		return
	}
	
	resp, err := p.esHTTPClient.Do(deleteReq)
	if err != nil {
		p.sendTUIError(clientConn, err)
		return
	}
	defer resp.Body.Close()
	
	if resp.StatusCode >= 400 {
		p.sendTUIError(clientConn, fmt.Errorf("delete failed: %s", resp.Status))
		return
	}
	
	// Invalidate cache
	p.commandCache.Invalidate()
	
	// Send success response
	success := map[string]interface{}{"deleted": true}
	p.sendTUIResponse(clientConn, success)
}

func (p *EnhancedTLSProxyWithCache) queryElasticsearchForCommands(query map[string]interface{}) (*QueryResponse, error) {
	searchURL := fmt.Sprintf("%s/totalrecall*/_search", p.esBaseURL)
	
	queryBytes, err := json.Marshal(query)
	if err != nil {
		return nil, err
	}
	
	req, err := http.NewRequest("POST", searchURL, bytes.NewReader(queryBytes))
	if err != nil {
		return nil, err
	}
	
	req.Header.Set("Content-Type", "application/json")
	
	resp, err := p.esHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("elasticsearch error: %s", resp.Status)
	}
	
	var esResponse struct {
		Hits struct {
			Total struct {
				Value int `json:"value"`
			} `json:"total"`
			Hits []struct {
				ID     string  `json:"_id"`
				Source Command `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	
	if err := json.NewDecoder(resp.Body).Decode(&esResponse); err != nil {
		return nil, err
	}
	
	// Convert to our format
	commands := make([]Command, len(esResponse.Hits.Hits))
	for i, hit := range esResponse.Hits.Hits {
		commands[i] = hit.Source
		commands[i].ID = hit.ID
	}

    // Determine if there are more results
    hasMore := false
    if from, ok := query["from"].(int); ok {
        if size, ok := query["size"].(int); ok {
            hasMore = (from + size) < esResponse.Hits.Total.Value
        }
    }
	
	return &QueryResponse{
		Commands: commands,
		Total:    esResponse.Hits.Total.Value,
		HasMore:  hasMore,
	}, nil
}

func (p *EnhancedTLSProxyWithCache) sendTUIResponse(conn net.Conn, data interface{}) {
	response, err := json.Marshal(data)
	if err != nil {
		debugLog("Failed to marshal TUI response: %v", err)
		return
	}
	
	conn.Write(append(response, '\n'))
}

func (p *EnhancedTLSProxyWithCache) sendTUIError(conn net.Conn, err error) {
	errorResp := map[string]interface{}{
		"error": err.Error(),
	}
	p.sendTUIResponse(conn, errorResp)
}

func (p *EnhancedTLSProxyWithCache) hashQuery(query map[string]interface{}) string {
	// Simple hash of query for caching
	data, _ := json.Marshal(query)
	return fmt.Sprintf("%x", data)[:16] // First 16 chars of hex
}

func (p *EnhancedTLSProxyWithCache) processFluentbitEventWithCache(data []byte) error {
	// Process normally via original fluent-bit logic
	err := p.processFluentbitEvent(data)
	
	// If this is a new command, invalidate cache
	var event map[string]interface{}
	if json.Unmarshal(data, &event) == nil {
		if _, hasCommand := event["command"]; hasCommand {
			// This is a new command, invalidate cache for fast refresh
			p.commandCache.Invalidate()
			debugLog("New command detected, invalidated cache")
		}
	}
	
	return err
}

func (p *EnhancedTLSProxyWithCache) processFluentbitEvent(data []byte) error {
	var testParse map[string]interface{}
	if err := json.Unmarshal(data, &testParse); err != nil {
		debugLog("Received invalid JSON, skipping: %v", err)
		return fmt.Errorf("invalid JSON: %v", err)
	}

	debugLog("Processing event: %s", string(data))

	// Check if this is a pub/sub-only event
	isPubSubOnly := false
	if pubsubOnly, exists := testParse["pubsub_only"]; exists {
		if pubsubOnlyBool, ok := pubsubOnly.(bool); ok && pubsubOnlyBool {
			isPubSubOnly = true
		}
	}

	if isPubSubOnly {
		debugLog("Processing as pub/sub-only event (not sending to fluent-bit)")
		p.pubsub.Publish(data)
		debugLog("Successfully published pub/sub-only event")
		return nil
	}

	// Regular event: send to both fluent-bit and pub/sub subscribers
	debugLog("Processing as regular event (sending to both fluent-bit and pub/sub)")

	// Send to fluent-bit
	conn, err := p.fluentbitPool.getConnection()
	if err != nil {
		p.fluentbitPool.mutex.Lock()
		p.fluentbitPool.totalErrors++
		p.fluentbitPool.mutex.Unlock()
		// Still try to publish to pub/sub even if fluent-bit fails
		p.pubsub.Publish(data)
		return err
	}

	var returnConn bool = true
	defer func() {
		if returnConn {
			p.fluentbitPool.returnConnection(conn)
		} else {
			conn.Close()
			p.fluentbitPool.mutex.Lock()
			p.fluentbitPool.activeConns--
			p.fluentbitPool.mutex.Unlock()
		}
	}()

	conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, err = conn.Write(append(data, '\n'))
	conn.SetWriteDeadline(time.Time{})

	if err != nil {
		debugLog("Failed to send to fluent-bit: %v", err)
		returnConn = false
		p.fluentbitPool.mutex.Lock()
		p.fluentbitPool.totalErrors++
		p.fluentbitPool.mutex.Unlock()
		// Still publish to pub/sub even if fluent-bit fails
		p.pubsub.Publish(data)
		return fmt.Errorf("failed to send data: %v", err)
	}

	p.fluentbitPool.mutex.Lock()
	p.fluentbitPool.totalSent++
	p.fluentbitPool.mutex.Unlock()

	// Send to pub/sub subscribers
	p.pubsub.Publish(data)

	debugLog("Successfully sent data to fluent-bit and published to pub/sub")
	return nil
}

func (p *EnhancedTLSProxyWithCache) prefetchDefaultQueries() {
	// Get current hostname for host-specific prefetching
	hostname, _ := os.Hostname()
	
	// Prefetch common queries for instant TUI loading
	defaultQueries := []map[string]interface{}{
		// Default query: recent commands, no filters
		{
			"from": 0,
			"size": 50,
			"sort": []map[string]interface{}{
				{"start_timestamp": map[string]interface{}{"order": "desc"}},
			},
		},
		// Host-filtered query
		{
			"from": 0,
			"size": 50,
			"sort": []map[string]interface{}{
				{"start_timestamp": map[string]interface{}{"order": "desc"}},
			},
			"query": map[string]interface{}{
				"bool": map[string]interface{}{
					"filter": []map[string]interface{}{
						{"term": map[string]interface{}{"hostname.keyword": hostname}},
					},
				},
			},
		},
	}
	
	for _, query := range defaultQueries {
		go func(q map[string]interface{}) {
			if response, err := p.queryElasticsearchForCommands(q); err == nil {
				queryHash := p.hashQuery(q)
				p.commandCache.Set(queryHash, response)
				debugLog("Prefetched query: %s (%d commands)", queryHash, len(response.Commands))
			}
		}(query)
	}
}

func (p *EnhancedTLSProxyWithCache) printStats(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fbActive, fbPooled, fbSent, fbErrors := p.fluentbitPool.GetStats()
			subscribers, totalEvents, totalSubs := p.pubsub.GetStats()

			log.Printf("Stats: FB(conns=%d,pooled=%d,sent=%d,err=%d) PubSub(subs=%d,events=%d,total_subs=%d)",
				fbActive, fbPooled, fbSent, fbErrors,
				subscribers, totalEvents, totalSubs)

			// Show cache stats
			p.commandCache.mutex.RLock()
			cacheSize := len(p.commandCache.entries)
			p.commandCache.mutex.RUnlock()
			
			log.Printf("Cache: %d entries", cacheSize)

			if subscribers > 0 {
				log.Printf("🚀 Reactive features active: %d subscriber(s) connected", subscribers)
			}
		}
	}
}

func (p *EnhancedTLSProxyWithCache) handleESRequest(clientConn net.Conn, reader *bufio.Reader, firstLine string) {
	debugLog("Handling HTTP request: %s", firstLine)
	
	httpReq, err := p.parseHTTPRequest(firstLine, reader)
	if err != nil {
		debugLog("Failed to parse HTTP request: %v", err)
		p.writeHTTPError(clientConn, 400, fmt.Sprintf("Bad request: %v", err))
		return
	}
	
	debugLog("Parsed HTTP request: %s %s", httpReq.Method, httpReq.URL.Path)
	
	targetURL := p.esBaseURL + httpReq.URL.Path
	if httpReq.URL.RawQuery != "" {
		targetURL += "?" + httpReq.URL.RawQuery
	}
	
	debugLog("Making HTTPS request to HAProxy: %s %s", httpReq.Method, targetURL)
	
	proxyReq, err := http.NewRequest(httpReq.Method, targetURL, httpReq.Body)
	if err != nil {
		debugLog("Failed to create proxy request: %v", err)
		p.writeHTTPError(clientConn, 500, "Failed to create request")
		return
	}
	
	// Copy headers
	for name, values := range httpReq.Header {
		for _, value := range values {
			proxyReq.Header.Add(name, value)
		}
	}
	
	proxyReq.Host = "elasticsearch"
	
	debugLog("Sending mTLS request to HAProxy...")
	
	resp, err := p.esHTTPClient.Do(proxyReq)
	if err != nil {
		debugLog("Failed to make mTLS request to HAProxy: %v", err)
		p.writeHTTPError(clientConn, 502, fmt.Sprintf("ES request failed: %v", err))
		return
	}
	defer resp.Body.Close()
	
	debugLog("Received response from HAProxy: %s", resp.Status)
	
	// Set a write deadline to detect if client disconnected
	clientConn.SetWriteDeadline(time.Now().Add(30 * time.Second))
	defer clientConn.SetWriteDeadline(time.Time{})
	
	err = p.writeHTTPResponse(clientConn, resp)
	if err != nil {
		debugLog("Failed to write response to client: %v", err)
		if strings.Contains(err.Error(), "broken pipe") {
			debugLog("Client disconnected before response could be written")
		}
		return
	}
	
	debugLog("HTTP request completed successfully")
}

func (p *EnhancedTLSProxyWithCache) parseHTTPRequest(firstLine string, reader *bufio.Reader) (*http.Request, error) {
	var requestData bytes.Buffer
	requestData.WriteString(firstLine + "\r\n")
	
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("failed to read request: %v", err)
		}
		
		requestData.WriteString(line)
		
		if line == "\r\n" || line == "\n" {
			break
		}
	}
	
	req, err := http.ReadRequest(bufio.NewReader(&requestData))
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTTP request: %v", err)
	}
	
	if req.ContentLength > 0 {
		body := make([]byte, req.ContentLength)
		_, err := io.ReadFull(reader, body)
		if err != nil {
			return nil, fmt.Errorf("failed to read request body: %v", err)
		}
		req.Body = io.NopCloser(bytes.NewReader(body))
	}
	
	return req, nil
}

func (p *EnhancedTLSProxyWithCache) writeHTTPResponse(clientConn net.Conn, resp *http.Response) error {
	statusLine := fmt.Sprintf("HTTP/1.1 %s\r\n", resp.Status)
	if _, err := clientConn.Write([]byte(statusLine)); err != nil {
		return err
	}
	
	for name, values := range resp.Header {
		for _, value := range values {
			headerLine := fmt.Sprintf("%s: %s\r\n", name, value)
			if _, err := clientConn.Write([]byte(headerLine)); err != nil {
				return err
			}
		}
	}
	
	if _, err := clientConn.Write([]byte("\r\n")); err != nil {
		return err
	}
	
	_, err := io.Copy(clientConn, resp.Body)
	return err
}

func (p *EnhancedTLSProxyWithCache) writeHTTPError(conn net.Conn, code int, message string) {
	response := fmt.Sprintf("HTTP/1.1 %d %s\r\nContent-Type: text/plain\r\nContent-Length: %d\r\n\r\n%s",
		code, http.StatusText(code), len(message), message)
	conn.Write([]byte(response))
}

func (p *EnhancedTLSProxyWithCache) handleSubscriber(clientConn net.Conn, subscriberID string, filterStr string) {
	defer clientConn.Close()

	filter := make(map[string]string)
	if filterStr != "" {
		pairs := strings.Split(filterStr, ",")
		for _, pair := range pairs {
			if kv := strings.SplitN(pair, "=", 2); len(kv) == 2 {
				filter[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
			}
		}
	}

	p.pubsub.Subscribe(subscriberID, clientConn, filter)
	defer p.pubsub.Unsubscribe(subscriberID)

	clientConn.Write([]byte(fmt.Sprintf("SUBSCRIBED %s\n", subscriberID)))

	scanner := bufio.NewScanner(clientConn)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "PING" {
			clientConn.Write([]byte("PONG\n"))
		} else if line == "QUIT" {
			break
		}
	}
}

func (p *EnhancedTLSProxyWithCache) Start(ctx context.Context) error {
	if err := os.RemoveAll(p.socketPath); err != nil {
		return fmt.Errorf("failed to remove existing socket: %v", err)
	}

	listener, err := net.Listen("unix", p.socketPath)
	if err != nil {
		return fmt.Errorf("failed to create unix socket: %v", err)
	}
	
	p.listener = listener

	if err := os.Chmod(p.socketPath, 0600); err != nil {
		log.Printf("Warning: failed to set socket permissions: %v", err)
	}

	log.Printf("Enhanced TLS proxy with caching listening on %s", p.socketPath)
	log.Printf("Fluent-bit target: %s", p.fluentbitPool.targetAddr)
	log.Printf("Elasticsearch target: %s (mTLS)", p.esBaseURL)
	log.Printf("Command cache enabled (5min TTL, 100 entries max)")
	
	if debugMode {
		log.Printf("Debug mode enabled")
	}

	// Start prefetching common queries
	go func() {
		time.Sleep(2 * time.Second) // Wait for ES to be ready
		p.prefetchDefaultQueries()
	}()

	go p.printStats(ctx)

	go func() {
		<-ctx.Done()
		debugLog("Context cancelled, closing listener")
		listener.Close()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				debugLog("Shutting down due to context cancellation")
				return ctx.Err()
			default:
				if ne, ok := err.(*net.OpError); ok && ne.Op == "accept" {
					debugLog("Listener closed, shutting down")
					return nil
				}
				debugLog("Failed to accept connection: %v", err)
				continue
			}
		}

		go p.handleClient(conn)
	}
}

func (p *EnhancedTLSProxyWithCache) Close() {
	debugLog("Closing enhanced TLS proxy...")
	
	if p.listener != nil {
		p.listener.Close()
	}
	
	close(p.fluentbitPool.connections)
	for conn := range p.fluentbitPool.connections {
		conn.Close()
	}
	
	debugLog("Enhanced TLS proxy closed")
}

func isHTTPRequest(line string) bool {
	methods := []string{"GET ", "POST ", "PUT ", "DELETE ", "HEAD ", "OPTIONS ", "PATCH "}
	for _, method := range methods {
		if strings.HasPrefix(line, method) {
			return true
		}
	}
	return false
}

func loadTLSConfig(caFile, certFile, keyFile string) (*tls.Config, error) {
	caCert, err := ioutil.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load CA certificate: %v", err)
	}

	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("failed to parse CA certificate")
	}

	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load client certificate: %v", err)
	}

	return &tls.Config{
		RootCAs:      caCertPool,
		Certificates: []tls.Certificate{cert},
		ServerName:   "haproxy",
		MinVersion:   tls.VersionTLS12,
	}, nil
}

func main() {
	var (
		socketPath = flag.String("socket", "/tmp/totalrecall-proxy.sock", "Unix domain socket path")
		
		fluentbitHost = flag.String("fluent-host", "127.0.0.1", "Fluent-bit host")
		fluentbitPort = flag.String("fluent-port", "5170", "Fluent-bit port")
		
		esHost = flag.String("es-host", "127.0.0.1", "Elasticsearch host")
		esPort = flag.String("es-port", "9243", "Elasticsearch port (via HAProxy)")
		
		poolSize = flag.Int("pool-size", 3, "TLS connection pool size per target")
		
		caFile   = flag.String("ca-file", "certs/ca.crt", "CA certificate file")
		certFile = flag.String("cert-file", "certs/client.crt", "Client certificate file")
		keyFile  = flag.String("key-file", "certs/client.key", "Client key file")
		
		esCaFile   = flag.String("es-ca-file", "", "ES CA certificate file (defaults to ca-file)")
		esCertFile = flag.String("es-cert-file", "", "ES client certificate file (defaults to cert-file)")
		esKeyFile  = flag.String("es-key-file", "", "ES client key file (defaults to key-file)")
		
		debug = flag.Bool("debug", false, "Enable debug logging")
	)
	flag.Parse()

	debugMode = *debug

	if *esCaFile == "" {
		*esCaFile = *caFile
	}
	if *esCertFile == "" {
		*esCertFile = *certFile
	}
	if *esKeyFile == "" {
		*esKeyFile = *keyFile
	}

	fluentbitTLS, err := loadTLSConfig(*caFile, *certFile, *keyFile)
	if err != nil {
		log.Fatalf("Failed to load fluent-bit TLS config: %v", err)
	}

	esTLS, err := loadTLSConfig(*esCaFile, *esCertFile, *esKeyFile)
	if err != nil {
		log.Fatalf("Failed to load elasticsearch TLS config: %v", err)
	}

	fluentbitAddr := fmt.Sprintf("%s:%s", *fluentbitHost, *fluentbitPort)
	esAddr := fmt.Sprintf("%s:%s", *esHost, *esPort)
	
	proxy := NewEnhancedTLSProxyWithCache(*socketPath, fluentbitAddr, esAddr, fluentbitTLS, esTLS, *poolSize)

	ctx, cancel := context.WithCancel(context.Background())

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	go func() {
		sig := <-sigChan
		log.Printf("Received signal %v, shutting down...", sig)
		cancel()
	}()

	err = proxy.Start(ctx)
	if err != nil && err != context.Canceled {
		log.Printf("Proxy error: %v", err)
	}

	proxy.Close()
	os.Remove(*socketPath)
	log.Println("Enhanced proxy with cache shutdown complete")
}
