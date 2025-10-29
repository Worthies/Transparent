# HTTPS MITM Proxy Usage Guide

## Overview

The transparent proxy now supports full HTTPS Man-In-The-Middle (MITM) interception. All HTTPS traffic passing through the proxy will be decrypted, inspected, saved to local files, and then re-encrypted to the destination.

## How It Works

1. Client sends CONNECT request to proxy
2. Proxy responds with "200 Connection established"
3. Proxy performs TLS handshake with client using self-signed certificate
4. All subsequent HTTPS requests are:
   - Decrypted by the proxy
   - Saved to local files with serial numbers
   - Forwarded to the destination server
   - Responses are saved and sent back to client

## File Storage

All requests and responses are saved to the `requests/` directory with the format:
```
<8-digit-serial>_<METHOD>_<path>.txt
```

Each file contains:
- Request headers
- Request body
- Response status code
- Response headers
- Response body

## Running the Proxy

```bash
# Build the proxy
go build -o transparent

# Run the proxy (default port 8080)
./transparent

# Run on a different port
./transparent -listen :9090
```

## Client Configuration

### Using curl with the proxy

```bash
# For HTTPS requests, you need to tell curl to accept the self-signed certificate
curl -x http://localhost:8080 --insecure https://api.example.com/data

# Or with explicit proxy settings
curl --proxy http://localhost:8080 --insecure https://httpbin.org/get
```

### Using wget with the proxy

```bash
# Set proxy environment variable
export http_proxy=http://localhost:8080
export https_proxy=http://localhost:8080

# Make requests (--no-check-certificate to ignore cert errors)
wget --no-check-certificate https://api.example.com/data
```

### Browser Configuration

1. Configure browser to use HTTP proxy at `localhost:8080`
2. Browser will show certificate warnings - accept them to proceed
3. All HTTPS traffic will be intercepted and saved

**Note:** Modern browsers may show security warnings about the self-signed certificate. You can:
- Accept the warning for each session
- Import the CA certificate (advanced users)

## Certificate Warnings

The proxy uses self-signed certificates for MITM. This will trigger security warnings in clients. This is **expected behavior** and the warnings can be safely ignored for testing/development purposes.

**DO NOT** use this proxy for production traffic or with sensitive data unless you understand the security implications.

## Checking Saved Requests

```bash
# List all saved requests
ls -la requests/

# View a specific request/response
cat requests/00000001_GET_api_users.txt

# Count total requests
ls requests/ | wc -l
```

## Example Output

```
requests/
├── 00000001_GET_httpbin_org_get.txt
├── 00000002_POST_api_example_com_users.txt
├── 00000003_GET_api_example_com_data.txt
└── ...
```

Each file contains both the request and response in a structured format for easy inspection.

## Security Notice

⚠️ **WARNING**: This is a MITM proxy that decrypts HTTPS traffic. Only use this for:
- Development and testing
- Network debugging
- Traffic analysis in controlled environments

**Never** use this to intercept traffic without proper authorization.
