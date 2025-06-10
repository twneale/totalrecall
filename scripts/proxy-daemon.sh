#!/bin/bash
# scripts/proxy-daemon.sh - Manage the TLS proxy daemon

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TOTALRECALL_ROOT="${TOTAL_RECALL_ROOT:-$(dirname "$SCRIPT_DIR")}"
PROXY_BINARY="$TOTALRECALL_ROOT/bin/tls-proxy"
SOCKET_PATH="/tmp/totalrecall-proxy.sock"
PID_FILE="/tmp/totalrecall-proxy.pid"
LOG_FILE="$HOME/.totalrecall/proxy.log"

# Configuration (can be overridden by environment variables)
PROXY_HOST="${PROXY_HOST:-127.0.0.1}"
PROXY_PORT="${PROXY_PORT:-5170}"
PROXY_POOL_SIZE="${PROXY_POOL_SIZE:-3}"
PROXY_DEBUG="${PROXY_DEBUG:-false}"

# Certificate paths
CA_FILE="${CA_FILE:-$HOME/.totalrecall/ca.crt}"
CERT_FILE="${CERT_FILE:-$HOME/.totalrecall/client.crt}"
KEY_FILE="${KEY_FILE:-$HOME/.totalrecall/client.key}"

usage() {
    cat << EOF
Usage: $0 {start|stop|restart|status|logs}

Commands:
    start       Start the TLS proxy daemon
    stop        Stop the TLS proxy daemon
    restart     Restart the TLS proxy daemon
    status      Show proxy status
    logs        Show recent proxy logs

Environment Variables:
    PROXY_HOST      Target host (default: 127.0.0.1)
    PROXY_PORT      Target port (default: 5170)
    PROXY_POOL_SIZE Connection pool size (default: 3)
    PROXY_DEBUG     Enable debug logging (default: false)
    CA_FILE         CA certificate file
    CERT_FILE       Client certificate file
    KEY_FILE        Client key file

Examples:
    $0 start                    # Start with defaults
    PROXY_DEBUG=true $0 start   # Start with debug logging
    $0 logs                     # View recent logs
EOF
}

# Check if proxy binary exists
check_binary() {
    if [[ ! -f "$PROXY_BINARY" ]]; then
        echo "❌ Proxy binary not found: $PROXY_BINARY"
        echo "Build it first: cd tools/tls-proxy && go build -o ../../bin/tls-proxy"
        exit 1
    fi
}

# Check if certificates exist
check_certificates() {
    local missing=0
    
    for cert in "$CA_FILE" "$CERT_FILE" "$KEY_FILE"; do
        if [[ ! -f "$cert" ]]; then
            echo "❌ Certificate file not found: $cert"
            missing=1
        fi
    done
    
    if [[ $missing -eq 1 ]]; then
        echo ""
        echo "Generate certificates first:"
        echo "  ./scripts/generate-certs.sh"
        echo "Or import existing certificates:"
        echo "  ./scripts/import-certs.sh"
        exit 1
    fi
}

# Get PID if proxy is running
get_pid() {
    if [[ -f "$PID_FILE" ]]; then
        local pid=$(cat "$PID_FILE")
        if kill -0 "$pid" 2>/dev/null; then
            echo "$pid"
            return 0
        else
            # Stale PID file
            rm -f "$PID_FILE"
        fi
    fi
    return 1
}

# Start the proxy daemon
start_proxy() {
    echo "🚀 Starting TLS proxy daemon..."
    
    # Check prerequisites
    check_binary
    check_certificates
    
    # Check if already running
    if get_pid >/dev/null; then
        echo "⚠️  Proxy is already running (PID: $(get_pid))"
        return 1
    fi
    
    # Ensure log directory exists
    mkdir -p "$(dirname "$LOG_FILE")"
    
    # Build command line arguments
    local args=(
        "--socket=$SOCKET_PATH"
        "--host=$PROXY_HOST"
        "--port=$PROXY_PORT"
        "--pool-size=$PROXY_POOL_SIZE"
        "--ca-file=$CA_FILE"
        "--cert-file=$CERT_FILE"
        "--key-file=$KEY_FILE"
    )
    
    # Add debug flag if enabled
    if [[ "$PROXY_DEBUG" == "true" ]]; then
        args+=("--debug")
        echo "🐛 Debug logging enabled"
    fi
    
    # Start the proxy in background
    echo "Starting proxy with args: ${args[*]}"
    nohup "$PROXY_BINARY" "${args[@]}" > "$LOG_FILE" 2>&1 &
    local pid=$!
    
    # Save PID
    echo "$pid" > "$PID_FILE"
    
    # Wait a moment and check if it started successfully
    sleep 2
    
    if kill -0 "$pid" 2>/dev/null; then
        echo "✅ Proxy started successfully (PID: $pid)"
        echo "📁 Socket: $SOCKET_PATH"
        echo "📋 Logs: $LOG_FILE"
        
        # Wait for socket to appear
        local retries=10
        while [[ $retries -gt 0 ]] && [[ ! -S "$SOCKET_PATH" ]]; do
            echo "   Waiting for socket... ($retries retries left)"
            sleep 1
            ((retries--))
        done
        
        if [[ -S "$SOCKET_PATH" ]]; then
            echo "✅ Socket ready: $SOCKET_PATH"
        else
            echo "⚠️  Socket not ready yet, check logs: tail -f $LOG_FILE"
        fi
    else
        echo "❌ Failed to start proxy"
        rm -f "$PID_FILE"
        echo "Check logs: tail -f $LOG_FILE"
        return 1
    fi
}

# Stop the proxy daemon
stop_proxy() {
    echo "🛑 Stopping TLS proxy daemon..."
    
    local pid
    if pid=$(get_pid); then
        echo "Stopping proxy (PID: $pid)"
        
        # Send SIGTERM first (graceful shutdown)
        kill -TERM "$pid"
        
        # Wait for graceful shutdown
        local retries=10
        while [[ $retries -gt 0 ]] && kill -0 "$pid" 2>/dev/null; do
            echo "   Waiting for graceful shutdown... ($retries)"
            sleep 1
            ((retries--))
        done
        
        # Force kill if still running
        if kill -0 "$pid" 2>/dev/null; then
            echo "   Force killing proxy..."
            kill -KILL "$pid"
            sleep 1
        fi
        
        # Cleanup
        rm -f "$PID_FILE"
        rm -f "$SOCKET_PATH"
        
        echo "✅ Proxy stopped"
    else
        echo "⚠️  Proxy is not running"
        
        # Clean up any stale socket
        if [[ -S "$SOCKET_PATH" ]]; then
            echo "   Removing stale socket: $SOCKET_PATH"
            rm -f "$SOCKET_PATH"
        fi
    fi
}

# Show proxy status
show_status() {
    echo "📊 TLS Proxy Status"
    echo "==================="
    
    local pid
    if pid=$(get_pid); then
        echo "Status: 🟢 Running"
        echo "PID: $pid"
        echo "Socket: $SOCKET_PATH"
        
        # Check socket
        if [[ -S "$SOCKET_PATH" ]]; then
            echo "Socket: ✅ Ready"
            echo "Socket permissions: $(ls -la "$SOCKET_PATH" | awk '{print $1, $3, $4}')"
        else
            echo "Socket: ❌ Not found"
        fi
        
        # Show process info
        echo ""
        echo "Process info:"
        ps -p "$pid" -o pid,ppid,user,start,time,comm 2>/dev/null || echo "  Process info unavailable"
        
        # Memory usage
        if command -v pmap >/dev/null 2>&1; then
            local memory=$(pmap "$pid" 2>/dev/null | tail -1 | awk '{print $2}' || echo "unknown")
            echo "Memory usage: $memory"
        fi
        
        # Test socket connectivity
        echo ""
        echo "Testing socket connectivity..."
        if echo "PING" | timeout 2 nc -U "$SOCKET_PATH" 2>/dev/null | grep -q "PONG"; then
            echo "Socket test: ✅ Responding"
        else
            echo "Socket test: ⚠️  Not responding (may be starting up)"
        fi
        
    else
        echo "Status: 🔴 Not running"
        echo "Socket: $(if [[ -S "$SOCKET_PATH" ]]; then echo "⚠️  Stale"; else echo "❌ Not found"; fi)"
    fi
    
    echo ""
    echo "Configuration:"
    echo "  Target: $PROXY_HOST:$PROXY_PORT"
    echo "  Pool size: $PROXY_POOL_SIZE"
    echo "  Debug: $PROXY_DEBUG"
    echo "  Log file: $LOG_FILE"
}

# Show recent logs
show_logs() {
    if [[ -f "$LOG_FILE" ]]; then
        echo "📋 Recent proxy logs:"
        echo "===================="
        tail -20 "$LOG_FILE"
        echo ""
        echo "To follow logs: tail -f $LOG_FILE"
    else
        echo "❌ Log file not found: $LOG_FILE"
    fi
}

# Restart proxy
restart_proxy() {
    echo "🔄 Restarting TLS proxy daemon..."
    stop_proxy
    sleep 2
    start_proxy
}

# Main command handling
case "${1:-}" in
    start)
        start_proxy
        ;;
    stop)
        stop_proxy
        ;;
    restart)
        restart_proxy
        ;;
    status)
        show_status
        ;;
    logs)
        show_logs
        ;;
    --help|-h|help)
        usage
        ;;
    "")
        echo "Error: No command specified"
        echo ""
        usage
        exit 1
        ;;
    *)
        echo "Error: Unknown command '$1'"
        echo ""
        usage
        exit 1
        ;;
esac
