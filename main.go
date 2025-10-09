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
	flag.StringVar(&listenAddr, "listen", ":8080", "Address to listen on")
	flag.Parse()

	// Set up logging
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	// Initialize infrastructure services
	proxySvc := infrastructure.NewProxyService()
	tlsSvc, err := infrastructure.NewTLSCertificateService()
	if err != nil {
		slog.Error("Failed to create TLS service", "error", err)
		os.Exit(1)
	}

	// Initialize application service
	appSvc := application.NewProxyApplicationService(proxySvc, tlsSvc)

	// Create HTTP server service
	httpSvc := infrastructure.NewHTTPServerService(appSvc)

	// Start the proxy
	slog.Info("Starting Transparent proxy", "address", listenAddr)
	fmt.Printf("Proxy starting on %s...\n", listenAddr)
	if err := httpSvc.Start(listenAddr); err != nil {
		slog.Error("Failed to start server", "error", err)
		os.Exit(1)
	}
}
