package infrastructure

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/worthies/transparent/internal/domain"
)

func TestProxyServiceImpl_HandleRequest(t *testing.T) {
	// Create a test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"message": "test response"}`))
	}))
	defer server.Close()

	// Create proxy service
	proxySvc := NewProxyService()

	// Create domain request
	req := &domain.Request{
		Method:  "GET",
		URL:     server.URL + "/test",
		Headers: http.Header{},
		Body:    []byte{},
	}

	// Handle request
	resp, err := proxySvc.HandleRequest(req, "00000001")
	if err != nil {
		t.Fatalf("HandleRequest failed: %v", err)
	}

	// Verify response
	if resp.StatusCode != 200 {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	expectedBody := `{"message": "test response"}`
	if string(resp.Body) != expectedBody {
		t.Errorf("Expected body %s, got %s", expectedBody, string(resp.Body))
	}

	if resp.Headers.Get("Content-Type") != "application/json" {
		t.Errorf("Expected Content-Type application/json, got %s", resp.Headers.Get("Content-Type"))
	}
}

func TestProxyServiceImpl_HandleRequestWithBody(t *testing.T) {
	// Create a test server that echoes the request body
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, r.ContentLength)
		r.Body.Read(body)
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(200)
		w.Write([]byte("echo: " + string(body)))
	}))
	defer server.Close()

	// Create proxy service
	proxySvc := NewProxyService()

	// Create domain request with body
	req := &domain.Request{
		Method:  "POST",
		URL:     server.URL + "/echo",
		Headers: http.Header{"Content-Type": []string{"text/plain"}},
		Body:    []byte("test message"),
	}

	// Handle request
	resp, err := proxySvc.HandleRequest(req, "00000001")
	if err != nil {
		t.Fatalf("HandleRequest failed: %v", err)
	}

	// Verify response
	if resp.StatusCode != 200 {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	expectedBody := "echo: test message"
	if string(resp.Body) != expectedBody {
		t.Errorf("Expected body %s, got %s", expectedBody, string(resp.Body))
	}
}

func TestProxyServiceImpl_InspectRequest(t *testing.T) {
	proxySvc := NewProxyService()

	req := &domain.Request{
		Method: "GET",
		URL:    "http://example.com",
	}

	result := proxySvc.InspectRequest(req)

	if result.Request != req {
		t.Errorf("Expected request to be returned in result")
	}

	if result.Blocked {
		t.Errorf("Expected request not to be blocked")
	}

	if result.Modified {
		t.Errorf("Expected request not to be modified")
	}
}

func TestProxyServiceImpl_InspectResponse(t *testing.T) {
	proxySvc := NewProxyService()

	resp := &domain.Response{
		StatusCode: 200,
		Body:       []byte("test"),
	}

	result := proxySvc.InspectResponse(resp)

	if result.Response != resp {
		t.Errorf("Expected response to be returned in result")
	}

	if result.Blocked {
		t.Errorf("Expected response not to be blocked")
	}

	if result.Modified {
		t.Errorf("Expected response not to be modified")
	}
}
