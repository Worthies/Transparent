package application

import (
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/worthies/transparent/internal/domain"
)

type mockProxyService struct{}

func (m *mockProxyService) HandleRequest(req *domain.Request, id string) (*domain.Response, error) {
	return &domain.Response{
		StatusCode: 200,
		Headers:    http.Header{"Content-Type": []string{"application/json"}},
		Body:       []byte(`{"status": "ok"}`),
		Timestamp:  time.Now(),
	}, nil
}

func (m *mockProxyService) HandleStreamingRequest(req *domain.Request, id string) (*domain.StreamingResponse, error) {
	// Return a streaming response with a string reader
	body := strings.NewReader(`{"status": "ok"}`)
	return &domain.StreamingResponse{
		StatusCode: 200,
		Headers:    http.Header{"Content-Type": []string{"application/json"}},
		BodyReader: io.NopCloser(body),
		Timestamp:  time.Now(),
	}, nil
}

func (m *mockProxyService) InspectRequest(req *domain.Request) *domain.InspectionResult {
	return &domain.InspectionResult{Request: req, Blocked: false}
}

func (m *mockProxyService) InspectResponse(resp *domain.Response) *domain.InspectionResult {
	return &domain.InspectionResult{Response: resp, Blocked: false}
}

func (m *mockProxyService) SaveRequestResponse(serial string, req *domain.Request, resp *domain.Response) {
	// Mock implementation - do nothing
}

type mockTLSCertService struct{}

func (m *mockTLSCertService) GenerateCertificate(host string) (*tls.Certificate, error) {
	return nil, nil
}

func (m *mockTLSCertService) GetCACertificate() (*tls.Certificate, error) {
	return nil, nil
}

func TestProxyApplicationService_HandleHTTPRequest(t *testing.T) {
	// Create mock services
	proxySvc := &mockProxyService{}
	tlsSvc := &mockTLSCertService{}

	// Create application service
	appSvc := NewProxyApplicationService(proxySvc, tlsSvc)

	// Create test request
	req := httptest.NewRequest("GET", "http://example.com/test", strings.NewReader("test body"))
	req.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()

	// Handle request
	appSvc.HandleHTTPRequest(w, req)

	// Check response
	if w.Code != 200 {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "ok") {
		t.Errorf("Expected response body to contain 'ok', got %s", body)
	}
}

func TestProxyApplicationService_UniqueIDs(t *testing.T) {
	proxySvc := &mockProxyService{}
	tlsSvc := &mockTLSCertService{}
	appSvc := NewProxyApplicationService(proxySvc, tlsSvc)

	// Make multiple requests and verify counter increments
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest("GET", "http://example.com/test", nil)
		w := httptest.NewRecorder()

		// Capture the ID by checking the service's counter
		initialCounter := appSvc.requestCounter
		appSvc.HandleHTTPRequest(w, req)
		finalCounter := appSvc.requestCounter

		if finalCounter <= initialCounter {
			t.Errorf("Request counter should have incremented")
		}

		// Since we can't easily capture the log output, we'll verify the counter increments
		if finalCounter-initialCounter != 1 {
			t.Errorf("Expected counter to increment by 1, got %d", finalCounter-initialCounter)
		}
	}
}

func TestProxyApplicationService_FormatBody(t *testing.T) {
	proxySvc := &mockProxyService{}
	tlsSvc := &mockTLSCertService{}
	appSvc := NewProxyApplicationService(proxySvc, tlsSvc)

	tests := []struct {
		name        string
		body        []byte
		contentType string
		expected    string
	}{
		{
			name:        "JSON text",
			body:        []byte(`{"key": "value"}`),
			contentType: "application/json",
			expected:    `{"key": "value"}`,
		},
		{
			name:        "HTML text",
			body:        []byte("<html><body>Hello</body></html>"),
			contentType: "text/html",
			expected:    "<html><body>Hello</body></html>",
		},
		{
			name:        "SSE events",
			body:        []byte("event: test\ndata: hello\n\n"),
			contentType: "text/event-stream",
			expected:    "[SSE: 1 events] event: test\ndata: hello",
		},
		{
			name:        "Binary data",
			body:        []byte{0x00, 0x01, 0x02},
			contentType: "application/octet-stream",
			expected:    "AAEC", // base64 of 0x00, 0x01, 0x02
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := appSvc.formatBody(tt.body, tt.contentType)
			if result != tt.expected {
				t.Errorf("formatBody() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestProxyApplicationService_IsTextContentType(t *testing.T) {
	proxySvc := &mockProxyService{}
	tlsSvc := &mockTLSCertService{}
	appSvc := NewProxyApplicationService(proxySvc, tlsSvc)

	tests := []struct {
		contentType string
		expected    bool
	}{
		{"text/plain", true},
		{"application/json", true},
		{"text/html", true},
		{"application/xml", true},
		{"text/event-stream", true},
		{"application/octet-stream", false},
		{"image/png", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.contentType, func(t *testing.T) {
			result := appSvc.isTextContentType(tt.contentType)
			if result != tt.expected {
				t.Errorf("isTextContentType(%s) = %v, expected %v", tt.contentType, result, tt.expected)
			}
		})
	}
}
