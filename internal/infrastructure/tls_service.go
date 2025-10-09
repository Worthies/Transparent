package infrastructure

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"time"
)

// TLSCertificateServiceImpl implements TLSCertificateService
type TLSCertificateServiceImpl struct {
	caCert *tls.Certificate
	caKey  *rsa.PrivateKey
}

// NewTLSCertificateService creates a new TLS certificate service
func NewTLSCertificateService() (*TLSCertificateServiceImpl, error) {
	// Generate CA certificate
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}

	caTemplate := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Transparent Proxy CA"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	caCertDER, err := x509.CreateCertificate(rand.Reader, &caTemplate, &caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, err
	}

	caCert, err := x509.ParseCertificate(caCertDER)
	if err != nil {
		return nil, err
	}

	tlsCert := &tls.Certificate{
		Certificate: [][]byte{caCertDER},
		PrivateKey:  caKey,
		Leaf:        caCert,
	}

	return &TLSCertificateServiceImpl{
		caCert: tlsCert,
		caKey:  caKey,
	}, nil
}

// GenerateCertificate generates a certificate for the given host
func (s *TLSCertificateServiceImpl) GenerateCertificate(host string) (*tls.Certificate, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject: pkix.Name{
			CommonName: host,
		},
		DNSNames:              []string{host},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, s.caCert.Leaf, &key.PublicKey, s.caKey)
	if err != nil {
		return nil, err
	}

	return &tls.Certificate{
		Certificate: [][]byte{certDER},
		PrivateKey:  key,
	}, nil
}

// GetCACertificate returns the CA certificate
func (s *TLSCertificateServiceImpl) GetCACertificate() (*tls.Certificate, error) {
	return s.caCert, nil
}
