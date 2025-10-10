package infrastructure

import (
	"bytes"
	"crypto/tls"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/worthies/transparent/internal/domain"
)

// ProxyServiceImpl implements the ProxyService
type ProxyServiceImpl struct {
	client *http.Client
}

// NewProxyService creates a new proxy service
func NewProxyService() *ProxyServiceImpl {
	return &ProxyServiceImpl{
		client: &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: true, // For MITM, we need to accept any cert
				},
			},
		},
	}
}

// HandleRequest forwards the request to the target server
func (s *ProxyServiceImpl) HandleRequest(req *domain.Request) (*domain.Response, error) {
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

		return &domain.Response{
			StatusCode: resp.StatusCode,
			Headers:    resp.Header.Clone(),
			Body:       body,
			Timestamp:  time.Now(),
		}, nil
	}

	// Read response body for regular responses
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return &domain.Response{
		StatusCode: resp.StatusCode,
		Headers:    resp.Header.Clone(),
		Body:       body,
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
