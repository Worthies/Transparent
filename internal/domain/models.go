package domain

import (
	"net/http"
	"time"
)

// Request represents an HTTP request in the proxy
type Request struct {
	ID        string
	Method    string
	URL       string
	Headers   http.Header
	Body      []byte
	Timestamp time.Time
	IsHTTPS   bool
	Host      string
}

// Response represents an HTTP response in the proxy
type Response struct {
	StatusCode int
	Headers    http.Header
	Body       []byte
	Timestamp  time.Time
}

// Proxy represents the proxy configuration
type Proxy struct {
	ListenAddr string
	CertFile   string
	KeyFile    string
	CAFile     string
}

// Server represents the builtin HTTPS server
type Server struct {
	CertFile string
	KeyFile  string
}

// InspectionResult represents the result of inspecting a request/response
type InspectionResult struct {
	Request  *Request
	Response *Response
	Modified bool
	Blocked  bool
}
