package infrastructure

import (
	"io"
	"net"
	"net/http"

	"github.com/worthies/transparent/internal/application"
)

// HTTPServerServiceImpl implements HTTPServerService
type HTTPServerServiceImpl struct {
	appSvc *application.ProxyApplicationService
}

// NewHTTPServerService creates a new HTTP server service
func NewHTTPServerService(appSvc *application.ProxyApplicationService) *HTTPServerServiceImpl {
	return &HTTPServerServiceImpl{
		appSvc: appSvc,
	}
}

// ServeHTTP handles HTTP requests
func (s *HTTPServerServiceImpl) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		// HTTPS CONNECT request - establish tunnel
		s.handleConnect(w, r)
	} else {
		// Regular HTTP request
		s.appSvc.HandleHTTPRequest(w, r)
	}
}

// handleConnect handles HTTPS CONNECT method for tunneling
func (s *HTTPServerServiceImpl) handleConnect(w http.ResponseWriter, r *http.Request) {
	// For HTTPS, we hijack the connection and establish a tunnel
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

	// Send 200 Connection established
	clientConn.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n"))

	// Connect to the target server
	targetConn, err := s.appSvc.ConnectToTarget(r.Host)
	if err != nil {
		clientConn.Close()
		return
	}

	// Start tunneling
	go s.tunnel(clientConn, targetConn)
	go s.tunnel(targetConn, clientConn)
}

// tunnel copies data between two connections
func (s *HTTPServerServiceImpl) tunnel(dst, src net.Conn) {
	defer dst.Close()
	defer src.Close()
	io.Copy(dst, src)
}

// Start starts the HTTP server
func (s *HTTPServerServiceImpl) Start(addr string) error {
	server := &http.Server{
		Addr:    addr,
		Handler: s,
	}

	return server.ListenAndServe()
}
