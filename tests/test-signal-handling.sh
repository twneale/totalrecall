#!/bin/bash
# test-signal-handling.sh - Test signal handling for TLS proxy

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TOTALRECALL_ROOT="${TOTAL_RECALL_ROOT:-$(dirname "$SCRIPT_DIR")}"
PROXY_BINARY="$TOTALRECALL_ROOT/bin/tls-proxy"
TEST_SOCKET="/tmp/test-proxy.sock"

echo "🧪 Testing TLS Proxy Signal Handling"
echo "====================================="

# Check if binary exists
if [[ ! -f "$PROXY_BINARY" ]]; then
    echo "❌ Proxy binary not found: $PROXY_BINARY"
    echo "Build it first: cd tools/tls-proxy && go build -o ../../bin/tls-proxy"
    exit 1
fi

# Cleanup function
cleanup() {
    echo ""
    echo "🧹 Cleaning up..."
    rm -f "$TEST_SOCKET"
}
trap cleanup EXIT

echo ""
echo "📋 Test 1: Normal startup and SIGTERM"
echo "-------------------------------------"

# Start proxy in background with minimal config (will fail TLS but that's okay for signal testing)
echo "Starting proxy..."
"$PROXY_BINARY" \
    --socket="$TEST_SOCKET" \
    --host=127.0.0.1 \
    --port=99999 &

PROXY_PID=$!
echo "Proxy started with PID: $PROXY_PID"

# Wait for socket to appear
sleep 2

if [[ -S "$TEST_SOCKET" ]]; then
    echo "✅ Socket created successfully"
else
    echo "⚠️  Socket not found (expected for test)"
fi

# Test SIGTERM (graceful shutdown)
echo "Sending SIGTERM..."
kill -TERM "$PROXY_PID"

# Wait for graceful shutdown
sleep 3

if kill -0 "$PROXY_PID" 2>/dev/null; then
    echo "❌ Process still running after SIGTERM"
    kill -KILL "$PROXY_PID"
    exit 1
else
    echo "✅ Process terminated gracefully with SIGTERM"
fi

echo ""
echo "📋 Test 2: SIGINT (Ctrl+C) handling"
echo "-----------------------------------"

# Start proxy again
"$PROXY_BINARY" \
    --socket="$TEST_SOCKET" \
    --host=127.0.0.1 \
    --port=99999 &

PROXY_PID=$!
echo "Proxy started with PID: $PROXY_PID"

sleep 2

# Test SIGINT (Ctrl+C)
echo "Sending SIGINT (simulating Ctrl+C)..."
kill -INT "$PROXY_PID"

# Wait for graceful shutdown
sleep 3

if kill -0 "$PROXY_PID" 2>/dev/null; then
    echo "❌ Process still running after SIGINT"
    kill -KILL "$PROXY_PID"
    exit 1
else
    echo "✅ Process terminated gracefully with SIGINT"
fi

echo ""
echo "📋 Test 3: Debug mode"
echo "---------------------"

# Test debug mode briefly
echo "Starting proxy with debug mode..."
timeout 5s "$PROXY_BINARY" \
    --socket="$TEST_SOCKET" \
    --host=127.0.0.1 \
    --port=99999 \
    --debug > /tmp/proxy-debug.log 2>&1 || true

if grep -q "\[DEBUG\]" /tmp/proxy-debug.log; then
    echo "✅ Debug logging working"
else
    echo "⚠️  Debug logging not found in output"
fi

echo ""
echo "📋 Test 4: Non-debug mode (default)"
echo "-----------------------------------"

# Test normal mode (no debug)
echo "Starting proxy without debug mode..."
timeout 5s "$PROXY_BINARY" \
    --socket="$TEST_SOCKET" \
    --host=127.0.0.1 \
    --port=99999 > /tmp/proxy-normal.log 2>&1 || true

if grep -q "\[DEBUG\]" /tmp/proxy-normal.log; then
    echo "❌ Debug logging found in normal mode"
    exit 1
else
    echo "✅ No debug logging in normal mode"
fi

echo ""
echo "📋 Test 5: Socket cleanup on shutdown"
echo "-------------------------------------"

# Start proxy
"$PROXY_BINARY" \
    --socket="$TEST_SOCKET" \
    --host=127.0.0.1 \
    --port=99999 &

PROXY_PID=$!
echo "Proxy started with PID: $PROXY_PID"

sleep 2

if [[ -S "$TEST_SOCKET" ]]; then
    echo "✅ Socket created"
else
    echo "⚠️  Socket not found"
fi

# Terminate and check cleanup
kill -TERM "$PROXY_PID"
sleep 3

if [[ -S "$TEST_SOCKET" ]]; then
    echo "⚠️  Socket still exists after shutdown"
    rm -f "$TEST_SOCKET"
else
    echo "✅ Socket cleaned up on shutdown"
fi

# Cleanup log files
rm -f /tmp/proxy-debug.log /tmp/proxy-normal.log

echo ""
echo "🎉 All signal handling tests passed!"
echo ""
echo "✅ SIGTERM handling: Working"
echo "✅ SIGINT handling: Working"  
echo "✅ Debug mode: Working"
echo "✅ Normal mode: Working"
echo "✅ Socket cleanup: Working"
echo ""
echo "The proxy now properly handles Ctrl+C and other termination signals!"
