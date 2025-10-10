package application

import (
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
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
	proxyService   domain.ProxyService
	tlsCertService domain.TLSCertificateService
	requestCounter int64
}

// NewProxyApplicationService creates a new proxy application service
func NewProxyApplicationService(
	proxySvc domain.ProxyService,
	tlsSvc domain.TLSCertificateService,
) *ProxyApplicationService {
	return &ProxyApplicationService{
		proxyService:   proxySvc,
		tlsCertService: tlsSvc,
	}
}

// HandleHTTPRequest handles incoming HTTP requests
func (s *ProxyApplicationService) HandleHTTPRequest(w http.ResponseWriter, r *http.Request) {
	// Generate unique request ID
	requestID := atomic.AddInt64(&s.requestCounter, 1) % 1000000 // 6-digit max
	idStr := fmt.Sprintf("%06d", requestID)

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

	// Handle the request
	resp, err := s.proxyService.HandleRequest(domainReq)
	if err != nil {
		color := getColorForID(idStr)
		fmt.Printf("%s%s Failed to handle request: %v%s\n", color, idStr, err, colorReset)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Log response
	s.logResponse(resp, idStr)

	// Inspect response
	respInspection := s.proxyService.InspectResponse(resp)
	if respInspection.Blocked {
		color := getColorForID(idStr)
		fmt.Printf("%s%s Response blocked: %s%s\n", color, idStr, "inspection", colorReset)
		http.Error(w, "Response blocked", http.StatusForbidden)
		return
	}

	// Send response
	s.sendResponse(w, resp)
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

func (s *ProxyApplicationService) logRequest(req *domain.Request, id string) {
	bodyStr := s.formatBody(req.Body, req.Headers.Get("Content-Type"))
	timestamp := time.Now().Format(time.RFC3339Nano)
	color := getColorForID(id)
	message := fmt.Sprintf("%s %s > %s %s %v %s",
		id,
		timestamp,
		req.Method,
		req.URL,
		req.Headers,
		bodyStr,
	)
	fmt.Printf("%s%s%s\n", color, message, colorReset)
}

func (s *ProxyApplicationService) logResponse(resp *domain.Response, id string) {
	contentType := resp.Headers.Get("Content-Type")
	contentType = strings.ToLower(contentType)

	// For streaming responses (SSE), log each line separately
	if strings.Contains(contentType, "text/event-stream") || s.isStreamingResponse(resp) {
		s.logStreamingResponse(resp, id)
		return
	}

	// Regular response logging
	bodyStr := s.formatBody(resp.Body, contentType)
	timestamp := time.Now().Format(time.RFC3339Nano)
	color := getColorForID(id)
	message := fmt.Sprintf("%s %s < %d %v %s",
		id,
		timestamp,
		resp.StatusCode,
		resp.Headers,
		bodyStr,
	)
	fmt.Printf("%s%s%s\n", color, message, colorReset)
}

func (s *ProxyApplicationService) logStreamingResponse(resp *domain.Response, id string) {
	bodyStr := string(resp.Body)
	lines := strings.Split(bodyStr, "\n")

	timestamp := time.Now().Format(time.RFC3339Nano)
	color := getColorForID(id)

	// Log headers first
	headerMessage := fmt.Sprintf("%s %s < %d %v streaming=true",
		id,
		timestamp,
		resp.StatusCode,
		resp.Headers,
	)
	fmt.Printf("%s%s%s\n", color, headerMessage, colorReset)

	// Log each line separately
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			lineTimestamp := time.Now().Format(time.RFC3339Nano)
			lineMessage := fmt.Sprintf("%s %s < %s",
				id,
				lineTimestamp,
				line,
			)
			fmt.Printf("%s%s%s\n", color, lineMessage, colorReset)
		}
	}
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
