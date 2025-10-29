#!/bin/bash
# Simple performance test for the transparent proxy

PROXY_PORT=1080
NUM_REQUESTS=100
CONCURRENT=10

echo "=== Transparent Proxy Performance Test ==="
echo ""
echo "Configuration:"
echo "  - Concurrent connections: $CONCURRENT"
echo "  - Total requests: $NUM_REQUESTS"
echo "  - Target: https://www.baidu.com"
echo ""

# Start the proxy
echo "Starting proxy on port $PROXY_PORT..."
go run . -listen :$PROXY_PORT > /tmp/proxy-perf.log 2>&1 &
PROXY_PID=$!
sleep 3

if ! ps -p $PROXY_PID > /dev/null; then
    echo "ERROR: Proxy failed to start"
    cat /tmp/proxy-perf.log
    exit 1
fi

echo "Proxy started (PID: $PROXY_PID)"
echo ""

# Warm up (prime connection pool and certificate cache)
echo "Warming up (3 requests)..."
for i in {1..3}; do
    curl -s -k --proxy http://127.0.0.1:$PROXY_PORT https://www.baidu.com > /dev/null
done
echo "Warm-up complete"
echo ""

# Run performance test
echo "Running performance test..."
START_TIME=$(date +%s.%N)

# Use GNU parallel if available, otherwise sequential
if command -v parallel &> /dev/null; then
    echo "Using GNU parallel for concurrent requests..."
    seq 1 $NUM_REQUESTS | parallel -j $CONCURRENT \
        "curl -s -k -w '%{time_total}\n' -o /dev/null --proxy http://127.0.0.1:$PROXY_PORT https://www.baidu.com" \
        > /tmp/perf-times.txt 2>/dev/null
else
    echo "GNU parallel not found, running sequential test..."
    for i in $(seq 1 $NUM_REQUESTS); do
        curl -s -k -w '%{time_total}\n' -o /dev/null --proxy http://127.0.0.1:$PROXY_PORT https://www.baidu.com >> /tmp/perf-times.txt 2>/dev/null
    done
fi

END_TIME=$(date +%s.%N)
TOTAL_TIME=$(echo "$END_TIME - $START_TIME" | bc)

# Calculate statistics
if [ -f /tmp/perf-times.txt ]; then
    TOTAL_REQUESTS=$(wc -l < /tmp/perf-times.txt)
    AVG_TIME=$(awk '{ total += $1; count++ } END { print total/count }' /tmp/perf-times.txt)
    MIN_TIME=$(sort -n /tmp/perf-times.txt | head -1)
    MAX_TIME=$(sort -n /tmp/perf-times.txt | tail -1)
    P50=$(sort -n /tmp/perf-times.txt | awk '{a[NR]=$1} END {print a[int(NR*0.5)]}')
    P95=$(sort -n /tmp/perf-times.txt | awk '{a[NR]=$1} END {print a[int(NR*0.95)]}')
    P99=$(sort -n /tmp/perf-times.txt | awk '{a[NR]=$1} END {print a[int(NR*0.99)]}')
    
    REQUESTS_PER_SEC=$(echo "scale=2; $TOTAL_REQUESTS / $TOTAL_TIME" | bc)
    
    echo ""
    echo "=== Results ==="
    echo "Total time: ${TOTAL_TIME}s"
    echo "Total requests: $TOTAL_REQUESTS"
    echo "Requests/sec: $REQUESTS_PER_SEC"
    echo ""
    echo "Latency (seconds):"
    echo "  Min:    $MIN_TIME"
    echo "  Avg:    $AVG_TIME"
    echo "  P50:    $P50"
    echo "  P95:    $P95"
    echo "  P99:    $P99"
    echo "  Max:    $MAX_TIME"
fi

# Stop the proxy
echo ""
echo "Stopping proxy..."
kill $PROXY_PID 2>/dev/null
wait $PROXY_PID 2>/dev/null

# Show file write statistics
echo ""
echo "=== File Write Statistics ==="
REQUEST_FILES=$(ls -1 requests/*.txt 2>/dev/null | wc -l)
echo "Files written: $REQUEST_FILES"

# Check proxy log for any warnings
if grep -q "Warning" /tmp/proxy-perf.log; then
    echo ""
    echo "=== Warnings from proxy log ==="
    grep "Warning" /tmp/proxy-perf.log | head -10
fi

# Clean up
rm -f /tmp/perf-times.txt

echo ""
echo "Test complete!"
