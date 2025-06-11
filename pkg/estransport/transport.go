package estransport

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// UnixSocketTransport implements http.RoundTripper for Unix socket connections
type UnixSocketTransport struct {
	socketPath string
	timeout    time.Duration
}

func NewUnixSocketTransport(socketPath string) *UnixSocketTransport {
	return &UnixSocketTransport{
		socketPath: socketPath,
		timeout:    30 * time.Second,
	}
}

// connCloser wraps a connection and implements io.ReadCloser for response bodies
type connCloser struct {
	io.Reader
	conn net.Conn
}

func (cc *connCloser) Close() error {
	return cc.conn.Close()
}

func (t *UnixSocketTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Connect to Unix socket
	conn, err := net.DialTimeout("unix", t.socketPath, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to proxy socket %s: %v", t.socketPath, err)
	}

	// Set overall timeout for the entire request/response cycle
	conn.SetDeadline(time.Now().Add(t.timeout))

	// Write the HTTP request to the socket
	if err := req.Write(conn); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to write HTTP request: %v", err)
	}

	// Read the HTTP response from the socket
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, req)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to read HTTP response: %v", err)
	}

	// CRITICAL FIX: Wrap the response body to ensure the connection
	// stays open until the body is fully read and closed
	if resp.Body != nil {
		resp.Body = &connCloser{
			Reader: resp.Body,
			conn:   conn,
		}
	} else {
		// No body, safe to close connection immediately
		conn.Close()
	}

	return resp, nil
}
