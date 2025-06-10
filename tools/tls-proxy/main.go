package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net"
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

type Subscriber struct {
	id     string
	conn   net.Conn
	writer *bufio.Writer
	filter map[string]string // Optional filters for events
}

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

	// Close existing subscription if it exists
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

	debugLog("Publishing to %d subscribers: %s", len(hub.subscribers), string(eventData))

	// Parse event to enable filtering
	var event map[string]interface{}
	json.Unmarshal(eventData, &event)

	deadSubs := []string{}

	for id, subscriber := range hub.subscribers {
		// Apply filters if any
		if !hub.matchesFilter(event, subscriber.filter) {
			continue
		}

		// Send event with timeout
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

	// Clean up dead subscribers
	for _, id := range deadSubs {
		hub.Unsubscribe(id)
	}

	hub.totalEvents++
}

func (hub *PubSubHub) matchesFilter(event map[string]interface{}, filter map[string]string) bool {
	if len(filter) == 0 {
		return true // No filter means match all
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

type TLSProxy struct {
	socketPath   string
	targetAddr   string
	tlsConfig    *tls.Config
	poolSize     int
	connections  chan *tls.Conn
	connMutex    sync.RWMutex
	activeConns  int
	totalSent    int64
	totalErrors  int64
	pubsub       *PubSubHub
	listener     net.Listener
}

func NewTLSProxy(socketPath, targetAddr string, tlsConfig *tls.Config, poolSize int) *TLSProxy {
	return &TLSProxy{
		socketPath:  socketPath,
		targetAddr:  targetAddr,
		tlsConfig:   tlsConfig,
		poolSize:    poolSize,
		connections: make(chan *tls.Conn, poolSize),
		pubsub:      NewPubSubHub(),
	}
}

// Get a connection from the pool or create a new one
func (p *TLSProxy) getConnection() (*tls.Conn, error) {
	select {
	case conn := <-p.connections:
		// Test if connection is still alive
		conn.SetDeadline(time.Now().Add(100 * time.Millisecond))
		_, err := conn.Write([]byte{}) // Empty write to test connection
		conn.SetDeadline(time.Time{})  // Clear deadline
		
		if err != nil {
			// Connection is dead, close it and create a new one
			conn.Close()
			p.connMutex.Lock()
			p.activeConns--
			p.connMutex.Unlock()
		} else {
			return conn, nil
		}
	default:
		// No connection available in pool
	}

	// Create new connection
	dialer := &net.Dialer{Timeout: 3 * time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", p.targetAddr, p.tlsConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create TLS connection: %v", err)
	}

	p.connMutex.Lock()
	p.activeConns++
	p.connMutex.Unlock()

	debugLog("Created new TLS connection (active: %d)", p.activeConns)
	return conn, nil
}

// Return a connection to the pool
func (p *TLSProxy) returnConnection(conn *tls.Conn) {
	select {
	case p.connections <- conn:
		debugLog("Returned connection to pool")
	default:
		// Pool is full, close the connection
		conn.Close()
		p.connMutex.Lock()
		p.activeConns--
		p.connMutex.Unlock()
		debugLog("Pool full, closed connection (active: %d)", p.activeConns)
	}
}

// Send data to fluent-bit
func (p *TLSProxy) sendToFluentBit(data []byte) error {
	conn, err := p.getConnection()
	if err != nil {
		p.totalErrors++
		return err
	}

	// Set write deadline to avoid hanging
	conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	
	// Send data
	_, err = conn.Write(append(data, '\n'))
	conn.SetWriteDeadline(time.Time{}) // Clear deadline
	
	if err != nil {
		// Connection failed, close it
		conn.Close()
		p.connMutex.Lock()
		p.activeConns--
		p.connMutex.Unlock()
		p.totalErrors++
		return fmt.Errorf("failed to send data: %v", err)
	}

	p.totalSent++
	p.returnConnection(conn)
	debugLog("Successfully sent data to fluent-bit")
	return nil
}

// Process incoming data (send to fluent-bit AND publish locally)
func (p *TLSProxy) processEvent(data []byte) error {
	// Validate JSON before processing
	var testParse map[string]interface{}
	if err := json.Unmarshal(data, &testParse); err != nil {
		debugLog("Received invalid JSON, skipping: %v", err)
		return fmt.Errorf("invalid JSON: %v", err)
	}
	
	debugLog("Processing event: %s", string(data))
	
	// Send to fluent-bit (for remote storage)
	fluentErr := p.sendToFluentBit(data)
	if fluentErr != nil {
		debugLog("Failed to send to fluent-bit: %v", fluentErr)
		// Don't return error - local pub/sub can still work
	}

	// Publish to local subscribers (for reactive TUI)
	p.pubsub.Publish(data)

	return fluentErr // Return fluent-bit error for statistics
}

// Handle subscription protocol
func (p *TLSProxy) handleSubscriber(clientConn net.Conn, subscriberID string, filterStr string) {
	defer clientConn.Close()

	// Parse filter
	filter := make(map[string]string)
	if filterStr != "" {
		pairs := strings.Split(filterStr, ",")
		for _, pair := range pairs {
			if kv := strings.SplitN(pair, "=", 2); len(kv) == 2 {
				filter[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
			}
		}
	}

	// Register subscriber
	p.pubsub.Subscribe(subscriberID, clientConn, filter)
	defer p.pubsub.Unsubscribe(subscriberID)

	// Send confirmation
	clientConn.Write([]byte(fmt.Sprintf("SUBSCRIBED %s\n", subscriberID)))

	// Keep connection alive and handle ping/pong
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

// Handle a client connection (publisher or subscriber)
func (p *TLSProxy) handleClient(clientConn net.Conn) {
	defer clientConn.Close()

	reader := bufio.NewReader(clientConn)
	
	// Read first line to determine if it's a subscriber or publisher
	firstLine, err := reader.ReadString('\n')
	if err != nil {
		return
	}

	firstLine = strings.TrimSpace(firstLine)

	// Check if it's a subscription request
	if strings.HasPrefix(firstLine, "SUBSCRIBE") {
		parts := strings.Fields(firstLine)
		subscriberID := "anonymous"
		filterStr := ""

		if len(parts) >= 2 {
			subscriberID = parts[1]
		}
		if len(parts) >= 3 {
			filterStr = strings.Join(parts[2:], " ")
		}

		debugLog("Handling subscriber: %s with filter: %s", subscriberID, filterStr)
		p.handleSubscriber(clientConn, subscriberID, filterStr)
		return
	}

	// Otherwise, treat as publisher - process the first line as an event
	if err := p.processEvent([]byte(firstLine)); err != nil {
		debugLog("Failed to process event: %v", err)
	}

	// Continue reading more events from this publisher
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		if err := p.processEvent(line); err != nil {
			debugLog("Failed to process event: %v", err)
			// Don't break the connection on send errors
		}
	}

	if err := scanner.Err(); err != nil {
		debugLog("Error reading from client: %v", err)
	}
}

// Start the proxy server
func (p *TLSProxy) Start(ctx context.Context) error {
	// Remove existing socket if it exists
	if err := os.RemoveAll(p.socketPath); err != nil {
		return fmt.Errorf("failed to remove existing socket: %v", err)
	}

	// Create unix domain socket
	listener, err := net.Listen("unix", p.socketPath)
	if err != nil {
		return fmt.Errorf("failed to create unix socket: %v", err)
	}
	
	// Store listener reference for cleanup
	p.listener = listener

	// Set socket permissions to be writable by owner only for security
	if err := os.Chmod(p.socketPath, 0600); err != nil {
		log.Printf("Warning: failed to set socket permissions: %v", err)
	}

	log.Printf("TLS proxy listening on %s", p.socketPath)
	log.Printf("Forwarding to fluent-bit: %s", p.targetAddr)
	if debugMode {
		log.Printf("Debug mode enabled")
	}

	// Start statistics goroutine
	go p.printStats(ctx)

	// Start goroutine to handle context cancellation
	go func() {
		<-ctx.Done()
		debugLog("Context cancelled, closing listener")
		listener.Close()
	}()

	// Accept connections
	for {
		conn, err := listener.Accept()
		if err != nil {
			// Check if we're shutting down
			select {
			case <-ctx.Done():
				debugLog("Shutting down due to context cancellation")
				return ctx.Err()
			default:
				// Check if error is due to listener being closed
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

// Print statistics periodically
func (p *TLSProxy) printStats(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.connMutex.RLock()
			activeConns := p.activeConns
			pooledConns := len(p.connections)
			totalSent := p.totalSent
			totalErrors := p.totalErrors
			p.connMutex.RUnlock()

			subscribers, totalEvents, totalSubs := p.pubsub.GetStats()

			log.Printf("Stats: tls_conns=%d, pooled=%d, sent=%d, errors=%d, subscribers=%d, events=%d, total_subs=%d",
				activeConns, pooledConns, totalSent, totalErrors, subscribers, totalEvents, totalSubs)
		}
	}
}

// Close all connections and cleanup
func (p *TLSProxy) Close() {
	debugLog("Closing TLS proxy...")
	
	// Close the listener if it exists
	if p.listener != nil {
		p.listener.Close()
	}
	
	// Close connection pool
	close(p.connections)
	for conn := range p.connections {
		conn.Close()
	}
	
	debugLog("TLS proxy closed")
}

func loadTLSConfig(caFile, certFile, keyFile string) (*tls.Config, error) {
	// Load CA cert
	caCert, err := ioutil.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load CA certificate: %v", err)
	}

	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("failed to parse CA certificate")
	}

	// Load client cert and key
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load client certificate: %v", err)
	}

	return &tls.Config{
		RootCAs:      caCertPool,
		Certificates: []tls.Certificate{cert},
		ServerName:   "fluentbit", // Should match the certificate
	}, nil
}

func main() {
	var (
		socketPath = flag.String("socket", "/tmp/totalrecall-proxy.sock", "Unix domain socket path")
		targetHost = flag.String("host", "127.0.0.1", "Fluent-bit host")
		targetPort = flag.String("port", "5170", "Fluent-bit port")
		poolSize   = flag.Int("pool-size", 3, "TLS connection pool size")
		caFile     = flag.String("ca-file", "certs/ca.crt", "CA certificate file")
		certFile   = flag.String("cert-file", "certs/client.crt", "Client certificate file")
		keyFile    = flag.String("key-file", "certs/client.key", "Client key file")
		debug      = flag.Bool("debug", false, "Enable debug logging")
	)
	flag.Parse()

	// Set debug mode from flag
	debugMode = *debug

	// Load TLS configuration
	tlsConfig, err := loadTLSConfig(*caFile, *certFile, *keyFile)
	if err != nil {
		log.Fatalf("Failed to load TLS config: %v", err)
	}

	// Create proxy
	targetAddr := fmt.Sprintf("%s:%s", *targetHost, *targetPort)
	proxy := NewTLSProxy(*socketPath, targetAddr, tlsConfig, *poolSize)

	// Create cancellable context
	ctx, cancel := context.WithCancel(context.Background())

	// Handle shutdown signals gracefully
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	// Start signal handler in goroutine
	go func() {
		sig := <-sigChan
		log.Printf("Received signal %v, shutting down...", sig)
		cancel() // Cancel context to stop all operations
	}()

	// Start proxy (this will block until context is cancelled)
	err = proxy.Start(ctx)
	if err != nil && err != context.Canceled {
		log.Printf("Proxy error: %v", err)
	}

	// Cleanup
	proxy.Close()
	os.Remove(*socketPath)
	log.Println("Proxy shutdown complete")
}
