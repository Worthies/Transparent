package domain

import (
	"crypto/tls"
	"net/http"
)

// ProxyService handles the core proxy logic
type ProxyService interface {
	HandleRequest(req *Request) (*Response, error)
	InspectRequest(req *Request) *InspectionResult
	InspectResponse(resp *Response) *InspectionResult
}

// TLSCertificateService handles TLS certificate generation for MITM
type TLSCertificateService interface {
	GenerateCertificate(host string) (*tls.Certificate, error)
	GetCACertificate() (*tls.Certificate, error)
}

// HTTPServerService handles the builtin HTTPS server
type HTTPServerService interface {
	ServeHTTP(w http.ResponseWriter, r *http.Request)
	Start(addr string) error
}
