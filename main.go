package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/worthies/transparent/internal/application"
	"github.com/worthies/transparent/internal/infrastructure"
)

func main() {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("Panic recovered", "panic", r)
			os.Exit(1)
		}
	}()

	var listenAddr string
	var enableKeepAlive bool
	flag.StringVar(&listenAddr, "listen", ":8080", "Address to listen on")
	flag.BoolVar(&enableKeepAlive, "keep-alive", false, "Enable HTTP keep-alive for TLS connections (reuse connections). Default: false (safer, close after each request)")
	flag.Parse()

	// Set up logging
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	// Log configuration
	if enableKeepAlive {
		fmt.Println("[CONFIG] HTTP Keep-Alive: ENABLED (performance mode - reuses connections)")
		fmt.Println("[CONFIG] Warning: May cause issues with non-compliant HTTP implementations")
	} else {
		fmt.Println("[CONFIG] HTTP Keep-Alive: DISABLED (safe mode - closes after each request)")
		fmt.Println("[CONFIG] Use -keep-alive flag to enable connection reuse for better performance")
	}

	// Initialize infrastructure services
	proxySvc := infrastructure.NewProxyService()
	tlsSvc, err := infrastructure.NewTLSCertificateService()
	if err != nil {
		slog.Error("Failed to create TLS service", "error", err)
		os.Exit(1)
	}

	// Initialize application service
	appSvc := application.NewProxyApplicationService(proxySvc, tlsSvc)

	// Create HTTP server service with configuration
	httpSvc := infrastructure.NewHTTPServerServiceWithConfig(appSvc, &infrastructure.HTTPServerConfig{
		EnableKeepAlive: enableKeepAlive,
	})

	// Start the proxy
	slog.Info("Starting Transparent proxy", "address", listenAddr)
	fmt.Printf("Proxy starting on %s...\n", listenAddr)
	if err := httpSvc.Start(listenAddr); err != nil {
		slog.Error("Failed to start server", "error", err)
		os.Exit(1)
	}
}
