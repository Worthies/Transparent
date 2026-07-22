package infrastructure

import (
	"bytes"
	"compress/gzip"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/worthies/transparent/internal/domain"
)

// Buffer pool for reusing byte buffers
var bufferPool = sync.Pool{
	New: func() interface{} {
		return new(bytes.Buffer)
	},
}

// ProxyServiceImpl implements the ProxyService
type ProxyServiceImpl struct {
	client      *http.Client
	fileWriteCh chan *fileWriteTask // Async file writing channel
	wg          sync.WaitGroup      // Wait group for graceful shutdown
}

type fileWriteTask struct {
	filePath string
	data     []byte
}

// NewProxyService creates a new proxy service
func NewProxyService() *ProxyServiceImpl {
	svc := &ProxyServiceImpl{
		client: &http.Client{
			Timeout: 30 * time.Second, // Overall request timeout
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: true, // For MITM, we need to accept any cert
				},
				DialContext: (&net.Dialer{
					Timeout:   10 * time.Second, // Connection timeout
					KeepAlive: 30 * time.Second,
				}).DialContext,
				TLSHandshakeTimeout:   10 * time.Second,
				ResponseHeaderTimeout: 10 * time.Second,
				ExpectContinueTimeout: 1 * time.Second,
				// Performance tuning for heavy load
				MaxIdleConns:        100,              // Max idle connections across all hosts
				MaxIdleConnsPerHost: 10,               // Max idle connections per host
				MaxConnsPerHost:     0,                // No limit on total connections per host
				IdleConnTimeout:     90 * time.Second, // How long idle connections stay open
				DisableCompression:  false,            // Allow compression
				ForceAttemptHTTP2:   true,             // Try HTTP/2
			},
		},
		fileWriteCh: make(chan *fileWriteTask, 1000), // Buffer up to 1000 pending writes
	}

	// Start async file writer workers (multiple workers for parallelism)
	for i := 0; i < 4; i++ {
		svc.wg.Add(1)
		go svc.fileWriter()
	}

	return svc
}

// fileWriter processes file write requests asynchronously
func (s *ProxyServiceImpl) fileWriter() {
	defer s.wg.Done()
	for task := range s.fileWriteCh {
		if err := os.WriteFile(task.filePath, task.data, 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to write file %s: %v\n", task.filePath, err)
		}
	}
}

// Close gracefully shuts down the proxy service
func (s *ProxyServiceImpl) Close() {
	close(s.fileWriteCh)
	s.wg.Wait()
}

// HandleRequest forwards the request to the target server
func (s *ProxyServiceImpl) HandleRequest(req *domain.Request, serial string) (*domain.Response, error) {

	// Create HTTP request
	httpReq, err := http.NewRequest(req.Method, req.URL, bytes.NewReader(req.Body))
	if err != nil {
		return nil, err
	}

	// Copy headers
	httpReq.Header = req.Headers.Clone()

	// Send request
	resp, err := s.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// For streaming responses (like SSE), capture initial content for logging
	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(strings.ToLower(contentType), "text/event-stream") {
		var buffer bytes.Buffer
		tempBuf := make([]byte, 32*1024)

		// Read what we can within the timeout without depending on
		// the concrete type of resp.Body (avoid unsafe type assertions).
		// We spawn a reader goroutine and use a timeout to stop waiting
		// for initial SSE bytes. No explicit cap on buffer size (user
		// requested unlimited buffering). The deadline is set to 5 minutes.

		done := make(chan struct{})
		go func() {
			defer close(done)
			for {
				n, err := resp.Body.Read(tempBuf)
				if n > 0 {
					buffer.Write(tempBuf[:n])
				}
				if err != nil {
					// EOF or read error -> stop reading
					return
				}
			}
		}()

		select {
		case <-done:
			// finished reading (EOF)
		case <-time.After(5 * time.Minute):
			// timeout after 5 minutes: close the body to unblock reader
			// and wait for goroutine to exit. Closing the body is the
			// safest portable way to interrupt the blocking Read.
			resp.Body.Close()
			<-done
		}

		body := buffer.Bytes()
		if len(body) == 0 {
			body = []byte("[SSE stream - no initial content captured]")
		}

		domainResp := &domain.Response{
			StatusCode: resp.StatusCode,
			Headers:    resp.Header.Clone(),
			Body:       body,
			Timestamp:  time.Now(),
		}

		// Save request and response to file (async)
		s.saveRequestToFileAsync(serial, req, domainResp)

		return domainResp, nil
	}

	// Read response body for regular responses
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	domainResp := &domain.Response{
		StatusCode: resp.StatusCode,
		Headers:    resp.Header.Clone(),
		Body:       body,
		Timestamp:  time.Now(),
	}

	// Save request and response to file (async)
	s.saveRequestToFileAsync(serial, req, domainResp)

	return domainResp, nil
}

// HandleStreamingRequest forwards the request and returns a streaming response
// This allows concurrent streaming to client and file for better performance
func (s *ProxyServiceImpl) HandleStreamingRequest(req *domain.Request, serial string) (*domain.StreamingResponse, error) {
	// Create HTTP request
	httpReq, err := http.NewRequest(req.Method, req.URL, bytes.NewReader(req.Body))
	if err != nil {
		return nil, err
	}

	// Copy headers
	httpReq.Header = req.Headers.Clone()

	// Send request
	resp, err := s.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	// Don't defer resp.Body.Close() - caller will handle it via StreamingResponse

	// Return streaming response - body will be read by caller
	return &domain.StreamingResponse{
		StatusCode: resp.StatusCode,
		Headers:    resp.Header.Clone(),
		BodyReader: resp.Body,
		Timestamp:  time.Now(),
	}, nil
}

// InspectRequest inspects the incoming request
func (s *ProxyServiceImpl) InspectRequest(req *domain.Request) *domain.InspectionResult {
	// Basic inspection - in real implementation, add filtering logic
	return &domain.InspectionResult{
		Request:  req,
		Modified: false,
		Blocked:  false,
	}
}

// InspectResponse inspects the outgoing response
func (s *ProxyServiceImpl) InspectResponse(resp *domain.Response) *domain.InspectionResult {
	// Basic inspection
	return &domain.InspectionResult{
		Response: resp,
		Modified: false,
		Blocked:  false,
	}
}

// SaveRequestResponse saves request/response to file (public interface)
func (s *ProxyServiceImpl) SaveRequestResponse(serial string, req *domain.Request, resp *domain.Response) {
	s.saveRequestToFileAsync(serial, req, resp)
}

// saveRequestToFileAsync queues request/response data for async file writing
func (s *ProxyServiceImpl) saveRequestToFileAsync(serial string, req *domain.Request, resp *domain.Response) {
	// Build file data in background to avoid blocking
	go func() {
		data, filePath := s.buildFileData(serial, req, resp)
		if data == nil {
			return
		}

		// Queue for async writing (non-blocking)
		select {
		case s.fileWriteCh <- &fileWriteTask{filePath: filePath, data: data}:
			// Successfully queued
		default:
			// Channel full, drop this write (or we could block here if we want backpressure)
			fmt.Fprintf(os.Stderr, "Warning: file write queue full, dropping request %s\n", serial)
		}
	}()
}

// buildFileData constructs the file content using buffer pool
func (s *ProxyServiceImpl) buildFileData(serial string, req *domain.Request, resp *domain.Response) ([]byte, string) {
	// Create requests directory if it doesn't exist (do this once, cache result)
	requestsDir := "requests"
	os.MkdirAll(requestsDir, 0755) // Ignore error, will fail on write if needed

	// Generate safe filename from method and path
	method := sanitizeFilename(req.Method)
	path := sanitizeFilename(req.URL)
	if path == "" {
		path = "root"
	}

	// Limit path length to avoid filesystem issues
	if len(path) > 100 {
		path = path[:100]
	}

	filename := fmt.Sprintf("%s_%s_%s.txt", serial, method, path)
	filePath := filepath.Join(requestsDir, filename)

	// Get buffer from pool
	buf := bufferPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer bufferPool.Put(buf)

	// Write request section
	buf.WriteString("========== REQUEST ==========\n")
	buf.WriteString(fmt.Sprintf("%s %s\n", req.Method, req.URL))
	buf.WriteString("\n--- Request Headers ---\n")
	for key, values := range req.Headers {
		for _, value := range values {
			buf.WriteString(fmt.Sprintf("%s: %s\n", key, value))
		}
	}
	buf.WriteString("\n--- Request Body ---\n")
	buf.Write(req.Body)

	// Write response section
	buf.WriteString("\n\n========== RESPONSE ==========\n")
	buf.WriteString(fmt.Sprintf("Status: %d\n", resp.StatusCode))
	buf.WriteString("\n--- Response Headers ---\n")
	for key, values := range resp.Headers {
		for _, value := range values {
			buf.WriteString(fmt.Sprintf("%s: %s\n", key, value))
		}
	}
	buf.WriteString("\n--- Response Body ---\n")
	body := resp.Body
	// Attempt gunzip if the response is gzip-compressed.
	if strings.EqualFold(resp.Headers.Get("Content-Encoding"), "gzip") {
		if r, err := gzip.NewReader(bytes.NewReader(body)); err == nil {
			if decompressed, err := io.ReadAll(r); err == nil {
				body = decompressed
			}
			r.Close()
		}
	}
	buf.Write(body)
	buf.WriteString("\n")

	// Make a copy of the data (buffer will be returned to pool)
	data := make([]byte, buf.Len())
	copy(data, buf.Bytes())

	return data, filePath
}

// saveRequestToFile saves request headers, body, response headers, and response body to a file
// Kept for backward compatibility, but not used in hot path anymore
func (s *ProxyServiceImpl) saveRequestToFile(serial string, req *domain.Request, resp *domain.Response) error {
	data, filePath := s.buildFileData(serial, req, resp)
	if data == nil {
		return fmt.Errorf("failed to build file data")
	}
	return os.WriteFile(filePath, data, 0644)
}

// sanitizeFilename removes characters unsafe for filenames
func sanitizeFilename(s string) string {
	// Replace unsafe characters with underscore
	reg := regexp.MustCompile(`[^a-zA-Z0-9._-]`)
	return reg.ReplaceAllString(s, "_")
}
