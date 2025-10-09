# Transparent Proxy

A Go-based proxy server that can inspect and forward HTTP and HTTPS requests using Domain-Driven Design (DDD) architecture.

## Features

- **HTTP Proxy**: Forwards HTTP requests to target servers
- **HTTPS Tunneling**: Handles HTTPS CONNECT requests for tunneling
- **Request Inspection**: Built-in hooks for inspecting and potentially modifying requests/responses
- **MITM Ready**: Infrastructure for Man-in-the-Middle HTTPS inspection (TLS certificate generation included)

## Installation

```bash
go install github.com/worthies/transparent@latest
```

## Testing with SSE Server

The project includes a test SSE server to verify logging functionality:

```bash
# Build the SSE server
go build -buildvcs=false ./cmd/sse-server

# Run the SSE server on port 8081
./sse-server
```

In another terminal, start the proxy:

```bash
# Run the transparent proxy on port 8080
go run . -listen=:8080
```

Configure your browser or curl to use `http://localhost:8080` as proxy, then visit `http://localhost:8081/events` to see SSE traffic logging.

## Features

- **HTTP Proxy**: Forwards HTTP requests to target servers
- **HTTPS Tunneling**: Handles HTTPS CONNECT requests for tunneling
- **Request Inspection**: Built-in hooks for inspecting and potentially modifying requests/responses
- **Traffic Logging**: Comprehensive logging with unique IDs, timestamps, and content analysis
- **SSE Support**: Special handling for Server-Sent Events streams
- **Content Detection**: Automatic detection of text vs binary content with appropriate formatting

## Architecture

This project follows Domain-Driven Design principles:

- **Domain**: Core business logic and entities
- **Application**: Orchestrates domain services
- **Infrastructure**: External concerns like HTTP handling and TLS

## Development

Clone the repository:

```bash
git clone https://github.com/worthies/transparent.git
cd transparent
go build .
```

## License

See LICENSE file.
