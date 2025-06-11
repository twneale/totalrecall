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

type Subscriber struct {
	id     string
	conn   net.Conn
	writer *bufio.Writer
	filter map[string]string
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

type EnhancedTLSProxy struct {
	socketPath     string
	fluentbitPool  *ConnectionPool
	esHTTPClient   *http.Client
	esBaseURL      string
	pubsub         *PubSubHub
	listener       net.Listener
}

func NewEnhancedTLSProxy(socketPath string, 
	fluentbitAddr, esAddr string,
	fluentbitTLS, esTLS *tls.Config,
	poolSize int) *EnhancedTLSProxy {
	
	esHTTPClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: esTLS,
		},
		Timeout: 30 * time.Second,
	}
	
	return &EnhancedTLSProxy{
		socketPath:    socketPath,
		fluentbitPool: NewConnectionPool(fluentbitAddr, fluentbitTLS, poolSize),
		esHTTPClient:  esHTTPClient,
		esBaseURL:     fmt.Sprintf("https://%s", esAddr),
		pubsub:        NewPubSubHub(),
	}
}

func (p *EnhancedTLSProxy) handleClient(clientConn net.Conn) {
	defer clientConn.Close()

	reader := bufio.NewReader(clientConn)
	
	firstLine, err := reader.ReadString('\n')
	if err != nil {
		debugLog("Failed to read first line: %v", err)
		return
	}

	firstLine = strings.TrimSpace(firstLine)
	debugLog("Received first line: %s", firstLine)

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
		if err := p.processFluentbitEvent([]byte(firstLine)); err != nil {
			debugLog("Failed to process fluent-bit event: %v", err)
		}
		
		scanner := bufio.NewScanner(reader)
		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}
			
			if err := p.processFluentbitEvent(line); err != nil {
				debugLog("Failed to process fluent-bit event: %v", err)
			}
		}
	}
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

func (p *EnhancedTLSProxy) handleESRequest(clientConn net.Conn, reader *bufio.Reader, firstLine string) {
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
	
	err = p.writeHTTPResponse(clientConn, resp)
	if err != nil {
		debugLog("Failed to write response to client: %v", err)
		return
	}
	
	debugLog("HTTP request completed successfully")
}

func (p *EnhancedTLSProxy) parseHTTPRequest(firstLine string, reader *bufio.Reader) (*http.Request, error) {
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

func (p *EnhancedTLSProxy) writeHTTPResponse(clientConn net.Conn, resp *http.Response) error {
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

func (p *EnhancedTLSProxy) writeHTTPError(conn net.Conn, code int, message string) {
	response := fmt.Sprintf("HTTP/1.1 %d %s\r\nContent-Type: text/plain\r\nContent-Length: %d\r\n\r\n%s",
		code, http.StatusText(code), len(message), message)
	conn.Write([]byte(response))
}

func (p *EnhancedTLSProxy) processFluentbitEvent(data []byte) error {
	var testParse map[string]interface{}
	if err := json.Unmarshal(data, &testParse); err != nil {
		debugLog("Received invalid JSON for fluent-bit, skipping: %v", err)
		return fmt.Errorf("invalid JSON: %v", err)
	}
	
	debugLog("Processing fluent-bit event: %s", string(data))
	
	conn, err := p.fluentbitPool.getConnection()
	if err != nil {
		p.fluentbitPool.mutex.Lock()
		p.fluentbitPool.totalErrors++
		p.fluentbitPool.mutex.Unlock()
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
		return fmt.Errorf("failed to send data: %v", err)
	}

	p.fluentbitPool.mutex.Lock()
	p.fluentbitPool.totalSent++
	p.fluentbitPool.mutex.Unlock()
	
	p.pubsub.Publish(data)
	
	debugLog("Successfully sent data to fluent-bit and published locally")
	return nil
}

func (p *EnhancedTLSProxy) handleSubscriber(clientConn net.Conn, subscriberID string, filterStr string) {
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

func (p *EnhancedTLSProxy) Start(ctx context.Context) error {
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

	log.Printf("Enhanced TLS proxy listening on %s", p.socketPath)
	log.Printf("Fluent-bit target: %s", p.fluentbitPool.targetAddr)
	log.Printf("Elasticsearch target: %s (mTLS)", p.esBaseURL)
	if debugMode {
		log.Printf("Debug mode enabled")
	}

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

func (p *EnhancedTLSProxy) printStats(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fbActive, fbPooled, fbSent, fbErrors := p.fluentbitPool.GetStats()
			subscribers, totalEvents, totalSubs := p.pubsub.GetStats()

			log.Printf("Stats: FB(conns=%d,pooled=%d,sent=%d,err=%d) ES(https_client) PubSub(subs=%d,events=%d,total_subs=%d)",
				fbActive, fbPooled, fbSent, fbErrors,
				subscribers, totalEvents, totalSubs)
		}
	}
}

func (p *EnhancedTLSProxy) Close() {
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
	
	proxy := NewEnhancedTLSProxy(*socketPath, fluentbitAddr, esAddr, fluentbitTLS, esTLS, *poolSize)

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
	log.Println("Enhanced proxy shutdown complete")
}
