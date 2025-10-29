package application

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/worthies/transparent/internal/domain"
)

// ANSI color codes
const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
	colorPurple = "\033[35m"
	colorCyan   = "\033[36m"
	// colorWhite  = "\033[37m"
)

var colors = []string{colorRed, colorGreen, colorYellow, colorBlue, colorPurple, colorCyan}

func getColorForID(id string) string {
	// Simple hash of the ID to get a consistent color
	hash := 0
	for _, char := range id {
		hash += int(char)
	}
	return colors[hash%len(colors)]
}

// ProxyApplicationService orchestrates the proxy operations
type ProxyApplicationService struct {
	proxyService      domain.ProxyService
	tlsCertService    domain.TLSCertificateService
	requestCounter    uint64 // atomic counter for 8-digit serial numbers (0-99999999)
	activeConnections int64  // atomic counter for active TLS connections
	activeRequests    int64  // atomic counter for active requests being processed
}

// NewProxyApplicationService creates a new proxy application service
func NewProxyApplicationService(
	proxySvc domain.ProxyService,
	tlsSvc domain.TLSCertificateService,
) *ProxyApplicationService {
	svc := &ProxyApplicationService{
		proxyService:   proxySvc,
		tlsCertService: tlsSvc,
	}

	// Start background goroutine to log active connections every 5 seconds
	go svc.logActiveConnectionsPeriodically()

	return svc
}

// IncrementConnection increments the active connection counter (called when TLS connection established)
func (s *ProxyApplicationService) IncrementConnection() {
	atomic.AddInt64(&s.activeConnections, 1)
}

// DecrementConnection decrements the active connection counter (called when TLS connection closed)
func (s *ProxyApplicationService) DecrementConnection() {
	atomic.AddInt64(&s.activeConnections, -1)
}

// logActiveConnectionsPeriodically logs the number of active connections every 5 seconds
func (s *ProxyApplicationService) logActiveConnectionsPeriodically() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		connections := atomic.LoadInt64(&s.activeConnections)
		requests := atomic.LoadInt64(&s.activeRequests)
		if connections > 0 || requests > 0 {
			fmt.Printf("\033[90m[Monitor] Active connections: %d | Pending requests: %d\033[0m\n", connections, requests)
		}
	}
}

// HandleHTTPRequest handles incoming HTTP requests with streaming support
func (s *ProxyApplicationService) HandleHTTPRequest(w http.ResponseWriter, r *http.Request) {
	// Increment active requests counter
	atomic.AddInt64(&s.activeRequests, 1)
	defer atomic.AddInt64(&s.activeRequests, -1)

	// Handle CORS preflight requests
	if r.Method == http.MethodOptions {
		s.handleCORSPreflight(w, r)
		return
	}

	// Generate serial number (8 digits with rollover)
	requestID := atomic.AddUint64(&s.requestCounter, 1) % 100000000

	// Generate unique request ID
	idStr := fmt.Sprintf("%08d", requestID)

	// Convert to domain request
	domainReq := s.convertToDomainRequest(r, idStr)

	// Log request
	s.logRequest(domainReq, idStr)

	// Inspect request
	inspection := s.proxyService.InspectRequest(domainReq)
	if inspection.Blocked {
		color := getColorForID(idStr)
		fmt.Printf("%s%s Request blocked: %s%s\n", color, idStr, "inspection", colorReset)
		http.Error(w, "Request blocked", http.StatusForbidden)
		return
	}

	// Handle the request with streaming
	streamResp, err := s.proxyService.HandleStreamingRequest(domainReq, idStr)
	if err != nil {
		color := getColorForID(idStr)
		fmt.Printf("%s%s Failed to handle request: %v%s\n", color, idStr, err, colorReset)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer streamResp.BodyReader.Close()

	// Hop-by-hop headers that should NOT be forwarded from upstream
	// These headers are specific to the connection between proxy and upstream server
	hopByHopHeaders := map[string]bool{
		"Connection":          true,
		"Keep-Alive":          true,
		"Proxy-Authenticate":  true,
		"Proxy-Authorization": true,
		"Te":                  true,
		"Trailer":             true,
		"Transfer-Encoding":   true,
		"Upgrade":             true,
	}

	// Write headers to client (excluding hop-by-hop headers)
	for k, v := range streamResp.Headers {
		// Skip hop-by-hop headers - let the HTTP server layer handle these
		if hopByHopHeaders[k] {
			continue
		}
		// Copy all other headers from upstream response
		w.Header()[k] = v
	}

	// Handle Content-Length properly for keep-alive connections
	// Some servers (like CDNs) send Ohc-File-Size instead of Content-Length
	// We need to convert this to Content-Length so the browser knows when the response ends
	contentLength := streamResp.Headers.Get("Content-Length")
	ohcFileSize := streamResp.Headers.Get("Ohc-File-Size")

	if contentLength == "" && ohcFileSize != "" {
		// Use Ohc-File-Size as Content-Length if Content-Length is missing
		w.Header().Set("Content-Length", ohcFileSize)
		color := getColorForID(idStr)
		fmt.Printf("%s%s [INFO] Using Ohc-File-Size (%s) as Content-Length%s\n",
			color, idStr, ohcFileSize, colorReset)
	}

	// Add CORS headers only if this is a cross-origin request and upstream didn't provide them
	if s.isCrossOriginRequest(domainReq) && w.Header().Get("Access-Control-Allow-Origin") == "" {
		// Get Origin header from request to set specific CORS origin
		origin := domainReq.Headers.Get("Origin")
		if origin == "" {
			origin = domainReq.Headers.Get("Referer")
			if origin == "" {
				origin = "*"
			}
		}

		// CRITICAL: Cannot use Access-Control-Allow-Credentials: true with Access-Control-Allow-Origin: *
		// This is a CORS spec violation that browsers will reject
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS, PATCH, HEAD")
		w.Header().Set("Access-Control-Allow-Headers", "*")
		w.Header().Set("Access-Control-Expose-Headers", "*")

		// Only set credentials if origin is specific (not *)
		if origin != "*" {
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}

		// Log that we're adding CORS headers
		color := getColorForID(idStr)
		contentType := streamResp.Headers.Get("Content-Type")
		fmt.Printf("%s%s [CORS] Added CORS headers (Origin: %s, Content-Type: %s, Status: %d)%s\n",
			color, idStr, origin, contentType, streamResp.StatusCode, colorReset)

		// Chrome ORB (Opaque Response Blocking) handling for application/json:
		//
		// ORB blocks certain MIME types (JSON, HTML, XML) in cross-origin requests to prevent
		// sensitive data leakage. However, ORB should NOT block if:
		// 1. Proper CORS headers are present (Access-Control-Allow-Origin matches)
		// 2. Request was made with 'cors' mode (not 'no-cors')
		// 3. Response has correct Content-Type
		//
		// For JSON specifically:
		// - Ensure Content-Type is exactly "application/json" (with charset if needed)
		// - Do NOT set X-Content-Type-Options: nosniff (allows browser to verify JSON)
		// - Ensure CORS headers are correct (origin must match, not *)

		// Ensure Content-Type is present
		if contentType == "" {
			w.Header().Set("Content-Type", "application/octet-stream")
		}

		// Add Timing-Allow-Origin to help with performance APIs
		if w.Header().Get("Timing-Allow-Origin") == "" {
			w.Header().Set("Timing-Allow-Origin", origin)
		}

		// Remove X-Content-Type-Options: nosniff for cross-origin JSON/HTML/XML
		// This allows the browser to verify the content matches the MIME type
		contentTypeLower := strings.ToLower(contentType)
		if strings.Contains(contentTypeLower, "json") ||
			strings.Contains(contentTypeLower, "html") ||
			strings.Contains(contentTypeLower, "xml") {
			w.Header().Del("X-Content-Type-Options")
		}
	}

	w.WriteHeader(streamResp.StatusCode)

	// Stream response body to both client and file concurrently
	s.streamResponseToClientAndFile(w, streamResp, domainReq, idStr)
}

// isCrossOriginRequest checks if the request is cross-origin by comparing Referer with Host
func (s *ProxyApplicationService) isCrossOriginRequest(req *domain.Request) bool {
	referer := req.Headers.Get("Referer")
	origin := req.Headers.Get("Origin")
	host := req.Host

	// If Origin header is present, use it (more reliable for CORS)
	if origin != "" {
		// Parse origin to get the host part
		// Origin format: "https://example.com" or "http://example.com:8080"
		originHost := s.extractHostFromURL(origin)
		if originHost != "" && originHost != host {
			return true
		}
	}

	// If no Origin but Referer is present, check referer
	if referer != "" {
		refererHost := s.extractHostFromURL(referer)
		if refererHost != "" && refererHost != host {
			return true
		}
	}

	return false
}

// extractHostFromURL extracts the host (including port) from a URL string
func (s *ProxyApplicationService) extractHostFromURL(urlStr string) string {
	// Remove protocol
	if strings.HasPrefix(urlStr, "https://") {
		urlStr = urlStr[8:]
	} else if strings.HasPrefix(urlStr, "http://") {
		urlStr = urlStr[7:]
	}

	// Extract host (everything before the first /)
	if idx := strings.Index(urlStr, "/"); idx != -1 {
		return urlStr[:idx]
	}
	return urlStr
}

// handleCORSPreflight handles CORS preflight OPTIONS requests
func (s *ProxyApplicationService) handleCORSPreflight(w http.ResponseWriter, r *http.Request) {
	// Set permissive CORS headers for preflight
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS, PATCH, HEAD")
	w.Header().Set("Access-Control-Allow-Headers", "*")
	w.Header().Set("Access-Control-Expose-Headers", "*")
	w.Header().Set("Access-Control-Max-Age", "86400") // 24 hours
	w.Header().Set("Access-Control-Allow-Credentials", "true")
	w.WriteHeader(http.StatusNoContent)
}

// ConnectToTarget connects to the target server for tunneling
func (s *ProxyApplicationService) ConnectToTarget(host string) (net.Conn, error) {
	return net.Dial("tcp", host)
}

// StartProxy starts the proxy server
func (s *ProxyApplicationService) StartProxy(config *domain.Proxy) error {
	// This method is not needed since server is started in infrastructure
	return nil
}

// GetTLSConfig returns the TLS config for HTTPS MITM
func (s *ProxyApplicationService) GetTLSConfig() (*tls.Config, error) {
	caCert, err := s.tlsCertService.GetCACertificate()
	if err != nil {
		return nil, err
	}

	return &tls.Config{
		Certificates: []tls.Certificate{*caCert},
		GetCertificate: func(chi *tls.ClientHelloInfo) (*tls.Certificate, error) {
			return s.tlsCertService.GenerateCertificate(chi.ServerName)
		},
	}, nil
}

func (s *ProxyApplicationService) convertToDomainRequest(r *http.Request, id string) *domain.Request {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		color := getColorForID(id)
		fmt.Printf("%s%s Failed to read request body: %v%s\n", color, id, err, colorReset)
		body = []byte{}
	}
	r.Body.Close()

	return &domain.Request{
		ID:        id,
		Method:    r.Method,
		URL:       r.URL.String(),
		Headers:   r.Header.Clone(),
		Body:      body,
		Timestamp: time.Now(),
		IsHTTPS:   r.TLS != nil,
		Host:      r.Host,
	}
}

func (s *ProxyApplicationService) sendResponse(w http.ResponseWriter, resp *domain.Response) {
	for k, v := range resp.Headers {
		w.Header()[k] = v
	}
	w.WriteHeader(resp.StatusCode)
	w.Write(resp.Body)
}

// streamResponseToClientAndFile streams response to both client and file concurrently
func (s *ProxyApplicationService) streamResponseToClientAndFile(
	w http.ResponseWriter,
	streamResp *domain.StreamingResponse,
	req *domain.Request,
	id string,
) {
	// Create a buffer to capture the response body for file writing
	var fileBuffer bytes.Buffer

	// Create a TeeReader that writes to both the client and our buffer
	teeReader := io.TeeReader(streamResp.BodyReader, &fileBuffer)

	// Stream to client with periodic flushing for better responsiveness
	bytesWritten, err := s.copyWithFlush(w, teeReader)
	if err != nil {
		color := getColorForID(id)
		fmt.Fprintf(os.Stderr, "%s%s Error streaming response: %v%s\n", color, id, err, colorReset)
	}

	// Final flush to ensure all data is sent
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}

	// Log response with actual size
	s.logStreamResponse(streamResp, id, int(bytesWritten))

	// Save to file asynchronously (don't block)
	go s.saveStreamToFile(id, req, streamResp, fileBuffer.Bytes())
}

// copyWithFlush copies data from reader to writer with periodic flushing
func (s *ProxyApplicationService) copyWithFlush(w http.ResponseWriter, r io.Reader) (int64, error) {
	flusher, canFlush := w.(http.Flusher)

	buf := make([]byte, 32*1024) // 32KB buffer
	var written int64

	for {
		nr, er := r.Read(buf)
		if nr > 0 {
			nw, ew := w.Write(buf[0:nr])
			if nw > 0 {
				written += int64(nw)
			}
			if ew != nil {
				return written, ew
			}
			if nr != nw {
				return written, io.ErrShortWrite
			}

			// Flush after each chunk for better streaming behavior
			if canFlush {
				flusher.Flush()
			}
		}
		if er != nil {
			if er != io.EOF {
				return written, er
			}
			break
		}
	}
	return written, nil
}

// saveStreamToFile saves the streamed response to a file
func (s *ProxyApplicationService) saveStreamToFile(
	id string,
	req *domain.Request,
	streamResp *domain.StreamingResponse,
	body []byte,
) {
	// Use the proxy service's file writing mechanism
	// Convert to domain.Response for compatibility
	resp := &domain.Response{
		StatusCode: streamResp.StatusCode,
		Headers:    streamResp.Headers,
		Body:       body,
		Timestamp:  streamResp.Timestamp,
	}

	// This will be handled by the proxy service's async file writer
	// For now, we can't directly call it, so we'll do a simple synchronous write
	// TODO: Refactor to use proper async mechanism
	s.saveResponseToFile(id, req, resp)
}

// saveResponseToFile is a helper to save response data
func (s *ProxyApplicationService) saveResponseToFile(serial string, req *domain.Request, resp *domain.Response) {
	// Delegate to the infrastructure layer's async file writer
	s.proxyService.SaveRequestResponse(serial, req, resp)
}

// logStreamResponse logs a streaming response
func (s *ProxyApplicationService) logStreamResponse(resp *domain.StreamingResponse, id string, bytesWritten int) {
	// Format: 2006-01-02T15:04:05.999999-07:00 (microseconds)
	timestamp := time.Now().Format("2006-01-02T15:04:05.000000-07:00")
	color := getColorForID(id)
	contentType := resp.Headers.Get("Content-Type")
	message := fmt.Sprintf("%s %s < %d [%s] size=%d bytes (streamed)",
		id,
		timestamp,
		resp.StatusCode,
		contentType,
		bytesWritten,
	)
	fmt.Printf("%s%s%s\n", color, message, colorReset)
}

func (s *ProxyApplicationService) logRequest(req *domain.Request, id string) {
	// Format: 2006-01-02T15:04:05.999999-07:00 (microseconds)
	timestamp := time.Now().Format("2006-01-02T15:04:05.000000-07:00")
	color := getColorForID(id)
	bodySize := len(req.Body)
	contentType := req.Headers.Get("Content-Type")
	message := fmt.Sprintf("%s %s > %s %s [%s] size=%d bytes",
		id,
		timestamp,
		req.Method,
		req.URL,
		contentType,
		bodySize,
	)
	fmt.Printf("%s%s%s\n", color, message, colorReset)
}

func (s *ProxyApplicationService) logResponse(resp *domain.Response, id string) {
	contentType := resp.Headers.Get("Content-Type")
	contentType = strings.ToLower(contentType)

	// For streaming responses (SSE), log without body content
	if strings.Contains(contentType, "text/event-stream") || s.isStreamingResponse(resp) {
		s.logStreamingResponse(resp, id)
		return
	}

	// Regular response logging - without body content
	// Format: 2006-01-02T15:04:05.999999-07:00 (microseconds)
	timestamp := time.Now().Format("2006-01-02T15:04:05.000000-07:00")
	color := getColorForID(id)
	bodySize := len(resp.Body)
	message := fmt.Sprintf("%s %s < %d [%s] size=%d bytes",
		id,
		timestamp,
		resp.StatusCode,
		contentType,
		bodySize,
	)
	fmt.Printf("%s%s%s\n", color, message, colorReset)
}

func (s *ProxyApplicationService) logStreamingResponse(resp *domain.Response, id string) {
	// Format: 2006-01-02T15:04:05.999999-07:00 (microseconds)
	timestamp := time.Now().Format("2006-01-02T15:04:05.000000-07:00")
	color := getColorForID(id)
	bodySize := len(resp.Body)
	contentType := resp.Headers.Get("Content-Type")

	// Log streaming response metadata without body content
	headerMessage := fmt.Sprintf("%s %s < %d [%s] streaming=true size=%d bytes",
		id,
		timestamp,
		resp.StatusCode,
		contentType,
		bodySize,
	)
	fmt.Printf("%s%s%s\n", color, headerMessage, colorReset)
}

func (s *ProxyApplicationService) isStreamingResponse(resp *domain.Response) bool {
	// Check for other streaming indicators
	contentType := strings.ToLower(resp.Headers.Get("Content-Type"))
	return strings.Contains(contentType, "stream") ||
		strings.Contains(contentType, "text/plain") && resp.StatusCode == 200
}

func (s *ProxyApplicationService) formatBody(body []byte, contentType string) string {
	if len(body) == 0 {
		return ""
	}

	contentType = strings.ToLower(contentType)

	// Handle SSE (Server-Sent Events)
	if strings.Contains(contentType, "text/event-stream") {
		return s.formatSSEBody(body)
	}

	// Check if text-based content type
	isText := s.isTextContentType(contentType)
	if isText {
		return string(body)
	} else {
		return base64.StdEncoding.EncodeToString(body)
	}
}

func (s *ProxyApplicationService) formatSSEBody(body []byte) string {
	bodyStr := string(body)
	lines := strings.Split(bodyStr, "\n")
	var events []string

	var currentEvent strings.Builder
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			// End of event
			if currentEvent.Len() > 0 {
				events = append(events, currentEvent.String())
				currentEvent.Reset()
			}
		} else {
			if currentEvent.Len() > 0 {
				currentEvent.WriteString("\n")
			}
			currentEvent.WriteString(line)
		}
	}

	// Handle last event if no trailing newline
	if currentEvent.Len() > 0 {
		events = append(events, currentEvent.String())
	}

	if len(events) == 0 {
		return "[SSE: no events]"
	}

	return fmt.Sprintf("[SSE: %d events] %s", len(events), strings.Join(events, " | "))
}

func (s *ProxyApplicationService) isTextContentType(contentType string) bool {
	contentType = strings.ToLower(contentType)
	textTypes := []string{
		"text/",
		"application/json",
		"application/xml",
		"application/xhtml",
		"application/rss",
		"application/atom",
		"application/javascript",
		"application/ecmascript",
		"text/event-stream", // SSE
	}

	for _, t := range textTypes {
		if strings.Contains(contentType, t) {
			return true
		}
	}
	return false
}
