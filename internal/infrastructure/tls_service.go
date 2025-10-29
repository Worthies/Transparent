package infrastructure

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"sync"
	"time"

	"software.sslmate.com/src/go-pkcs12"
)

// TLSCertificateServiceImpl implements TLSCertificateService
type TLSCertificateServiceImpl struct {
	caCert    *tls.Certificate
	caKey     *rsa.PrivateKey
	certCache sync.Map // Cache of generated certificates by hostname
}

const (
	caCertFile     = "transparent-ca.crt"
	caKeyFile      = "transparent-ca.key"
	caP12File      = "transparent-ca.p12"
	caPassword     = "changeit"
	caCertValidity = 365 * 24 * time.Hour // 1 year
)

// NewTLSCertificateService creates a new TLS certificate service
func NewTLSCertificateService() (*TLSCertificateServiceImpl, error) {
	// Try to load existing certificate and key
	caCert, caKey, err := loadCACertificateFromFile()
	if err == nil {
		fmt.Println("✓ Loaded existing CA certificate from disk")
		fmt.Println("  - transparent-ca.crt (PEM format - for Firefox, Linux, macOS)")
		fmt.Println("  - transparent-ca.p12 (PKCS12 format - for Chrome, Windows)")
		fmt.Println("  Password for .p12 file: changeit")
		fmt.Println("\n  Certificate valid until:", caCert.NotAfter.Format(time.RFC1123))

		tlsCert := &tls.Certificate{
			Certificate: [][]byte{caCert.Raw},
			PrivateKey:  caKey,
			Leaf:        caCert,
		}

		return &TLSCertificateServiceImpl{
			caCert: tlsCert,
			caKey:  caKey,
		}, nil
	}

	// Generate new CA certificate if not found or invalid
	fmt.Println("Generating new CA certificate...")

	caKey, err = rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}

	caTemplate := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Transparent Proxy CA"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(caCertValidity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	caCertDER, err := x509.CreateCertificate(rand.Reader, &caTemplate, &caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, err
	}

	parsedCert, err := x509.ParseCertificate(caCertDER)
	if err != nil {
		return nil, err
	}

	tlsCert := &tls.Certificate{
		Certificate: [][]byte{caCertDER},
		PrivateKey:  caKey,
		Leaf:        parsedCert,
	}

	// Save CA certificate and key to files for reuse
	if err := saveCACertificateToFile(caCertDER, caKey); err != nil {
		// Log warning but don't fail - this is not critical
		fmt.Fprintf(os.Stderr, "Warning: failed to save CA certificate to file: %v\n", err)
	} else {
		fmt.Println("✓ CA certificate saved to:")
		fmt.Println("  - transparent-ca.crt (PEM format - for Firefox, Linux, macOS)")
		fmt.Println("  - transparent-ca.p12 (PKCS12 format - for Chrome, Windows)")
		fmt.Println("  Password for .p12 file: changeit")
		fmt.Println("\n  Import the appropriate certificate into your browser/system to avoid certificate warnings.")
		fmt.Println("  See CERTIFICATE_IMPORT.md for detailed instructions.")
		fmt.Println("  Certificate valid until:", parsedCert.NotAfter.Format(time.RFC1123))
	}

	return &TLSCertificateServiceImpl{
		caCert: tlsCert,
		caKey:  caKey,
	}, nil
}

// GenerateCertificate generates a certificate for the given host
// Uses caching to avoid regenerating certificates for the same host
func (s *TLSCertificateServiceImpl) GenerateCertificate(host string) (*tls.Certificate, error) {
	// Check cache first
	if cached, ok := s.certCache.Load(host); ok {
		cert := cached.(*tls.Certificate)
		// Check if certificate is still valid (not expired)
		if cert.Leaf != nil && time.Now().Before(cert.Leaf.NotAfter) {
			return cert, nil
		}
		// Certificate expired, remove from cache
		s.certCache.Delete(host)
	}

	// Generate new certificate
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}

	// Use random serial number for uniqueness
	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName: host,
		},
		DNSNames:              []string{host},
		NotBefore:             time.Now().Add(-5 * time.Minute), // Small margin for clock skew
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, s.caCert.Leaf, &key.PublicKey, s.caKey)
	if err != nil {
		return nil, err
	}

	// Parse certificate to get Leaf
	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return nil, err
	}

	tlsCert := &tls.Certificate{
		Certificate: [][]byte{certDER},
		PrivateKey:  key,
		Leaf:        cert,
	}

	// Store in cache
	s.certCache.Store(host, tlsCert)

	return tlsCert, nil
}

// GetCACertificate returns the CA certificate
func (s *TLSCertificateServiceImpl) GetCACertificate() (*tls.Certificate, error) {
	return s.caCert, nil
}

// saveCACertificateToFile saves the CA certificate and private key in multiple formats
func saveCACertificateToFile(certDER []byte, privateKey *rsa.PrivateKey) error {
	// Parse certificate for PKCS12
	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return fmt.Errorf("failed to parse certificate: %w", err)
	}

	// 1. Save PEM certificate
	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certDER,
	})

	if err := os.WriteFile(caCertFile, certPEM, 0644); err != nil {
		return fmt.Errorf("failed to write PEM certificate file: %w", err)
	}

	// 2. Save PEM private key
	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	})

	if err := os.WriteFile(caKeyFile, keyPEM, 0600); err != nil {
		return fmt.Errorf("failed to write private key file: %w", err)
	}

	// 3. Save PKCS12 format (for Chrome, Windows)
	pfxData, err := pkcs12.Modern.Encode(privateKey, cert, nil, caPassword)
	if err != nil {
		return fmt.Errorf("failed to encode PKCS12: %w", err)
	}

	if err := os.WriteFile(caP12File, pfxData, 0644); err != nil {
		return fmt.Errorf("failed to write PKCS12 certificate file: %w", err)
	}

	return nil
}

// loadCACertificateFromFile loads the CA certificate and private key from disk
func loadCACertificateFromFile() (*x509.Certificate, *rsa.PrivateKey, error) {
	// Check if files exist
	if _, err := os.Stat(caCertFile); os.IsNotExist(err) {
		return nil, nil, fmt.Errorf("certificate file not found")
	}
	if _, err := os.Stat(caKeyFile); os.IsNotExist(err) {
		return nil, nil, fmt.Errorf("key file not found")
	}

	// Load certificate
	certPEM, err := os.ReadFile(caCertFile)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read certificate file: %w", err)
	}

	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil {
		return nil, nil, fmt.Errorf("failed to decode PEM certificate")
	}

	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse certificate: %w", err)
	}

	// Check if certificate is expired or will expire soon (within 7 days)
	if time.Now().After(cert.NotAfter) {
		return nil, nil, fmt.Errorf("certificate has expired")
	}
	if time.Now().Add(7 * 24 * time.Hour).After(cert.NotAfter) {
		fmt.Println("Warning: Certificate will expire soon:", cert.NotAfter.Format(time.RFC1123))
	}

	// Load private key
	keyPEM, err := os.ReadFile(caKeyFile)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read key file: %w", err)
	}

	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, nil, fmt.Errorf("failed to decode PEM private key")
	}

	privateKey, err := x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse private key: %w", err)
	}

	return cert, privateKey, nil
}
