#!/bin/bash

# Configuration sync service - monitors config changes and auto-updates template

CONFIG_FILE="$HOME/.totalrecall/env-config.json"
ELASTICSEARCH_URL="${ELASTICSEARCH_URL:-http://localhost:9200}"
CHECK_INTERVAL="${CHECK_INTERVAL:-300}"  # Check every 5 minutes
LOCK_FILE="/tmp/totalrecall-sync.lock"

log() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] $1"
}

cleanup() {
    rm -f "$LOCK_FILE"
    exit 0
}

trap cleanup EXIT INT TERM

# Prevent multiple instances
if [[ -f "$LOCK_FILE" ]]; then
    PID=$(cat "$LOCK_FILE")
    if kill -0 "$PID" 2>/dev/null; then
        log "Another sync process is running (PID: $PID)"
        exit 1
    else
        rm -f "$LOCK_FILE"
    fi
fi

echo $$ > "$LOCK_FILE"

log "Starting Total Recall configuration sync service"
log "Monitoring: $CONFIG_FILE"
log "Elasticsearch: $ELASTICSEARCH_URL"
log "Check interval: ${CHECK_INTERVAL}s"

# Get initial file modification time
if [[ -f "$CONFIG_FILE" ]]; then
    LAST_MTIME=$(stat -c %Y "$CONFIG_FILE" 2>/dev/null || stat -f %m "$CONFIG_FILE" 2>/dev/null)
else
    LAST_MTIME=0
fi

while true; do
    # Check if config file was modified
    if [[ -f "$CONFIG_FILE" ]]; then
        CURRENT_MTIME=$(stat -c %Y "$CONFIG_FILE" 2>/dev/null || stat -f %m "$CONFIG_FILE" 2>/dev/null)
        
        if [[ "$CURRENT_MTIME" -gt "$LAST_MTIME" ]]; then
            log "Configuration file changed, checking for template update..."
            
            # Validate config first
            if jq empty "$CONFIG_FILE" 2>/dev/null; then
                log "Configuration file is valid JSON"
                
                # Check if template update is needed
                if ./scripts/update-elasticsearch-template.sh --check-only; then
                    log "Template is up to date"
                else
                    log "Template update required, applying changes..."
                    if ./scripts/update-elasticsearch-template.sh --backup; then
                        log "✅ Template updated successfully"
                        
                        # Optional: Trigger drift monitoring
                        if command -v ./scripts/drift-monitor.sh >/dev/null 2>&1; then
                            log "Running drift analysis..."
                            ./scripts/drift-monitor.sh
                        fi
                    else
                        log "❌ Template update failed"
                    fi
                fi
                
                LAST_MTIME="$CURRENT_MTIME"
            else
                log "❌ Configuration file has invalid JSON, skipping update"
            fi
        fi
    else
        log "⚠️  Configuration file not found: $CONFIG_FILE"
    fi
    
    sleep "$CHECK_INTERVAL"
done
