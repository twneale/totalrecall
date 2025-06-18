#!/bin/bash
# Launcher script for Total Recall History TUI

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TOTALRECALL_ROOT="${TOTAL_RECALL_ROOT:-$(dirname "$SCRIPT_DIR")}"
SOCKET_PATH="${SOCKET_PATH:-/tmp/totalrecall-proxy.sock}"

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

echo -e "${GREEN}🚀 Total Recall History TUI${NC}"
echo "==========================="
echo ""

# Check if TUI binary exists
if [[ ! -f "$TOTALRECALL_ROOT/bin/history-tui" ]]; then
    echo -e "${RED}❌ TUI binary not found${NC}"
    echo "Build it first: ./scripts/setup-tui-system.sh"
    exit 1
fi

# Check if proxy is running
if [[ ! -S "$SOCKET_PATH" ]]; then
    echo -e "${YELLOW}⚠️  TLS proxy not running. Starting it now...${NC}"
    
    if [[ -f "$TOTALRECALL_ROOT/scripts/proxy-daemon.sh" ]]; then
        "$TOTALRECALL_ROOT/scripts/proxy-daemon.sh" start
        
        # Wait for socket to appear
        for i in {1..10}; do
            if [[ -S "$SOCKET_PATH" ]]; then
                break
            fi
            echo "   Waiting for proxy... ($i/10)"
            sleep 1
        done
        
        if [[ ! -S "$SOCKET_PATH" ]]; then
            echo -e "${RED}❌ Failed to start TLS proxy${NC}"
            echo "Please start it manually:"
            echo "   $TOTALRECALL_ROOT/scripts/proxy-daemon.sh start"
            exit 1
        fi
    else
        echo -e "${RED}❌ Proxy daemon script not found${NC}"
        echo "Make sure Total Recall is properly set up"
        exit 1
    fi
fi

echo -e "${GREEN}✅ TLS proxy running${NC}"
echo "   Socket: $SOCKET_PATH"
echo ""

# Check if elasticsearch is accessible via proxy
echo "🔍 Testing Elasticsearch connectivity..."
if timeout 3 bash -c "</dev/tcp/127.0.0.1/9200" 2>/dev/null; then
    echo -e "${GREEN}✅ Elasticsearch accessible${NC}"
else
    echo -e "${YELLOW}⚠️  Elasticsearch may not be running${NC}"
    echo "Consider starting: docker-compose up -d elasticsearch"
fi

echo ""
echo -e "${GREEN}📺 Starting History TUI...${NC}"
echo ""
echo "Key bindings:"
echo "  j/k or ↑/↓    Navigate commands"
echo "  b/f           Page up/down"
echo "  /             Search"
echo "  h/s/p         Toggle host/shell/PWD filters"
echo "  e             Edit command in vim"
echo "  c             Copy command"
echo "  x             Execute command"
echo "  d             Delete command"
echo "  f             Fuzzy find (if fzf available)"
echo "  ?             Show help"
echo "  q             Quit"
echo ""
echo "Press any key to continue..."
read -n 1 -s

# Clear screen and run the TUI
clear
exec "$TOTALRECALL_ROOT/bin/history-tui" "$SOCKET_PATH"
