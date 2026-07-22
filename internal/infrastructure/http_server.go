package infrastructure

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/worthies/transparent/internal/application"
)

// HTTPServerConfig holds configuration for the HTTP server
type HTTPServerConfig struct {
	EnableKeepAlive bool // Whether to enable HTTP keep-alive for TLS connections
}

// HTTPServerServiceImpl implements HTTPServerService
type HTTPServerServiceImpl struct {
	appSvc *application.ProxyApplicationService
	config *HTTPServerConfig
}

// NewHTTPServerService creates a new HTTP server service with default config (keep-alive disabled)
func NewHTTPServerService(appSvc *application.ProxyApplicationService) *HTTPServerServiceImpl {
	return NewHTTPServerServiceWithConfig(appSvc, &HTTPServerConfig{
		EnableKeepAlive: false, // Default: safe mode
	})
}

// NewHTTPServerServiceWithConfig creates a new HTTP server service with custom configuration
func NewHTTPServerServiceWithConfig(appSvc *application.ProxyApplicationService, config *HTTPServerConfig) *HTTPServerServiceImpl {
	return &HTTPServerServiceImpl{
		appSvc: appSvc,
		config: config,
	}
}

// ServeHTTP handles HTTP requests
func (s *HTTPServerServiceImpl) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		// HTTPS CONNECT request - establish tunnel
		s.handleConnect(w, r)
	} else {
		// Regular HTTP request - track it as a connection
		s.appSvc.IncrementConnection()
		defer s.appSvc.DecrementConnection()

		s.appSvc.HandleHTTPRequest(w, r)
	}
}

// handleConnect handles HTTPS CONNECT method with MITM interception
func (s *HTTPServerServiceImpl) handleConnect(w http.ResponseWriter, r *http.Request) {
	// Hijack the connection
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "Hijacking not supported", http.StatusInternalServerError)
		return
	}

	clientConn, _, err := hj.Hijack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer clientConn.Close()

	// Send 200 Connection established to the client
	_, err = clientConn.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n"))
	if err != nil {
		return
	}

	// Get TLS config with dynamically generated certificate
	tlsConfig, err := s.appSvc.GetTLSConfig()
	if err != nil {
		fmt.Fprintf(clientConn, "HTTP/1.1 500 Internal Server Error\r\n\r\nFailed to get TLS config: %v", err)
		return
	}

	// Wrap the client connection with TLS using our self-signed certificate
	tlsConn := tls.Server(clientConn, tlsConfig)
	err = tlsConn.Handshake()
	if err != nil {
		return // TLS handshake failed
	}
	defer tlsConn.Close()

	// Now read HTTP requests from the TLS connection and process them
	// Create an HTTP server to handle requests over this TLS connection
	s.serveTLSConnection(tlsConn, r.Host)
}

// serveTLSConnection serves HTTP requests over a TLS connection
func (s *HTTPServerServiceImpl) serveTLSConnection(tlsConn *tls.Conn, originalHost string) {
	// Increment connection counter when TLS connection established
	s.appSvc.IncrementConnection()
	defer s.appSvc.DecrementConnection()

	reader := bufio.NewReader(tlsConn)

	// Handle requests over the TLS connection
	// By default, close after each request (safe mode) to prevent issues with non-compliant implementations
	for {
		// Read the HTTP request from the TLS connection
		req, err := http.ReadRequest(reader)
		if err != nil {
			// Connection closed or error reading request
			return
		}

		// Fix the request URL - when HTTPS is decrypted, the URL is relative
		// We need to reconstruct it as an absolute URL
		if !req.URL.IsAbs() {
			// Relative URL - build absolute URL
			req.URL.Scheme = "https"
			req.URL.Host = originalHost
		}

		// Ensure Host header is set correctly
		if req.Host == "" {
			req.Host = originalHost
		}

		// Create a response writer that writes back to the TLS connection
		responseWriter := &tlsResponseWriter{
			conn:   tlsConn,
			header: make(http.Header),
		}

		// Connection management strategy based on configuration
		var shouldClose bool
		connectionHeader := strings.ToLower(req.Header.Get("Connection"))

		// Keep-Alive ENABLED (performance mode): Follow HTTP/1.1 spec
		// Keep connections alive by default, close only if requested
		if req.ProtoMajor == 1 && req.ProtoMinor >= 1 {
			// HTTP/1.1: keep-alive by default
			if connectionHeader == "close" {
				shouldClose = true
			} else {
				shouldClose = false
			}
		} else {
			// HTTP/1.0 or unknown: close by default unless explicitly keep-alive
			if connectionHeader == "keep-alive" {
				shouldClose = false
			} else {
				shouldClose = true
			}
		}

		// Handle the request through our normal proxy flow
		s.appSvc.HandleHTTPRequest(responseWriter, req)

		// Ensure response is flushed
		responseWriter.Flush()

		// Close connection if needed
		if !s.config.EnableKeepAlive || shouldClose {
			return
		}
	}
}

// tlsResponseWriter implements http.ResponseWriter for TLS connections
type tlsResponseWriter struct {
	conn        net.Conn
	header      http.Header
	statusCode  int
	wroteHeader bool
}

func (w *tlsResponseWriter) Header() http.Header {
	return w.header
}

func (w *tlsResponseWriter) WriteHeader(statusCode int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.statusCode = statusCode

	// Write status line
	statusText := http.StatusText(statusCode)
	if statusText == "" {
		statusText = "Unknown"
	}
	fmt.Fprintf(w.conn, "HTTP/1.1 %d %s\r\n", statusCode, statusText)

	// Write headers
	w.header.Write(w.conn)
	fmt.Fprintf(w.conn, "\r\n")
}

func (w *tlsResponseWriter) Write(data []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.conn.Write(data)
}

// Flush ensures all data is written to the connection
func (w *tlsResponseWriter) Flush() {
	// For TLS connections, we need to ensure the data is actually written
	// net.Conn doesn't buffer, but TLS might, so force headers to be written
	if _, ok := w.conn.(*tls.Conn); ok {
		// For TLS connections, ensure headers are written if they haven't been
		if !w.wroteHeader {
			w.WriteHeader(http.StatusOK)
		}
	}

	// Also check if the connection itself has a Flush method
	if flusher, ok := w.conn.(interface{ Flush() error }); ok {
		flusher.Flush()
	}
}

// Start starts the proxy using a raw net.Listener so CONNECT tunnels work
// reliably across all Go versions. Incoming raw bytes are logged to
// /tmp/proxy_raw_requests.log for debugging.
func (s *HTTPServerServiceImpl) Start(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go s.handleRawConn(conn)
	}
}

func (s *HTTPServerServiceImpl) handleRawConn(conn net.Conn) {
	defer conn.Close()
	peer := conn.RemoteAddr().String()

	// Peek at the first bytes the client sends.
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 512)
	n, _ := conn.Read(buf)
	conn.SetReadDeadline(time.Time{})

	if n > 0 {
		f, _ := os.OpenFile("/tmp/proxy_raw.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if f != nil {
			fmt.Fprintf(f, "\n=== %s @ %s ===\n%s\n", peer, time.Now().Format(time.RFC3339), string(buf[:n]))
			f.Close()
		}
		// If it's not HTTP, bail.
		if n < 4 || !strings.HasPrefix(string(buf[:n]), "CONN") && !strings.HasPrefix(string(buf[:n]), "GET ") &&
			!strings.HasPrefix(string(buf[:n]), "POST") && !strings.HasPrefix(string(buf[:n]), "HEAD") &&
			!strings.HasPrefix(string(buf[:n]), "PUT ") && !strings.HasPrefix(string(buf[:n]), "DELETE") &&
			!strings.HasPrefix(string(buf[:n]), "OPTI") && !strings.HasPrefix(string(buf[:n]), "PATC") {
			fmt.Fprintf(os.Stderr, "[proxy] non-HTTP from %s: %q\n", peer, string(buf[:min(n, 100)]))
			conn.Write([]byte("HTTP/1.1 501 Not Implemented\r\n\r\n"))
			return
		}
	}

	// Rebuild the reader with the peeked bytes + the rest of the conn.
	reader := bufio.NewReader(io.MultiReader(strings.NewReader(string(buf[:n])), conn))
	for {
		req, err := http.ReadRequest(reader)
		if err != nil {
			return
		}
		rw := &connResponseWriter{conn: conn}
		s.ServeHTTP(rw, req)
		if !s.config.EnableKeepAlive {
			return
		}
		if strings.EqualFold(req.Header.Get("Connection"), "close") {
			return
		}
	}
}

// connResponseWriter is a minimal http.ResponseWriter + http.Hijacker.
type connResponseWriter struct {
	conn        net.Conn
	header      http.Header
	wroteHeader bool
}

func (w *connResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}
func (w *connResponseWriter) WriteHeader(statusCode int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	statusText := http.StatusText(statusCode)
	if statusText == "" {
		statusText = "Unknown"
	}
	fmt.Fprintf(w.conn, "HTTP/1.1 %d %s\r\n", statusCode, statusText)
	w.header.Write(w.conn)
	fmt.Fprintf(w.conn, "\r\n")
}
func (w *connResponseWriter) Write(data []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.conn.Write(data)
}
func (w *connResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.conn, bufio.NewReadWriter(bufio.NewReader(w.conn), bufio.NewWriter(w.conn)), nil
}
