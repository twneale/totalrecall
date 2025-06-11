# Updated preexec.sh with pub/sub events for reactive UI

function preexec() { 
  # Capture PWD before command execution (race condition fix)
  export ___PREEXEC_PWD="$(pwd)"
  
  # Get preexec data (now includes command_id)
  export ___PREEXEC_DATA="$($TOTAL_RECALL_ROOT/bin/preexec-hook "$1")"
  
  # Extract command_id from the preexec data for correlation
  # The data is base64 encoded JSON, so we need to decode and parse it
  if [[ -n "$___PREEXEC_DATA" ]]; then
    # Decode base64 and extract command_id using jq (if available) or a simple approach
    if command -v jq >/dev/null 2>&1; then
      export ___COMMAND_ID="$(echo "$___PREEXEC_DATA" | base64 -d | jq -r '.command_id' 2>/dev/null)"
    else
      # Fallback: extract command_id with basic text processing
      export ___COMMAND_ID="$(echo "$___PREEXEC_DATA" | base64 -d | grep -o '"command_id":"[^"]*"' | cut -d'"' -f4)"
    fi
    
    # Send preexec event to pub/sub (non-blocking)
    # This shows the command immediately in the UI (grey/pending state)
    ($TOTAL_RECALL_ROOT/bin/preexec-hook --send-preexec-event 2>/dev/null &)
  fi
}

function precmd () {
  local ___RETURN_CODE=$?
  
  # Update lf (file manager) if available
  (lf -remote "send cd $___PREEXEC_PWD; set sortby time; set info time" 2>/dev/null &)
  
  # Send precmd event to pub/sub first (non-blocking)
  # This updates the command color in the UI based on return code
  if [[ -n "$___COMMAND_ID" ]]; then
    (export ___RETURN_CODE="$___RETURN_CODE"; \
     $TOTAL_RECALL_ROOT/bin/preexec-hook --send-precmd-event 2>/dev/null &)
  fi
  
  # Continue with existing fluent-bit logic (for persistent storage)
  # Try socket first (fast path), fall back to TLS if proxy is down
  ($TOTAL_RECALL_ROOT/bin/precmd-hook \
      -preexec-data="$___PREEXEC_DATA" \
      -return-code="$___RETURN_CODE" \
      -env-config="$HOME/.totalrecall/env-config.json" \
      --use-socket \
      --socket-path="/tmp/totalrecall-proxy.sock" \
      2>/dev/null || \
   $TOTAL_RECALL_ROOT/bin/preexec-hook \
      -preexec-data="$___PREEXEC_DATA" \
      -return-code="$___RETURN_CODE" \
      -env-config="$HOME/.totalrecall/env-config.json" \
      --tls \
      --tls-ca-file="$HOME/.totalrecall/ca.crt" \
      --tls-cert-file="$HOME/.totalrecall/client.crt" \
      --tls-key-file="$HOME/.totalrecall/client.key" &);
      
  # Clean up environment variables
  unset ___PREEXEC_DATA;
  unset ___COMMAND_ID;
}

# Auto-start proxy if it's not running (optional)
ensure_proxy_running() {
  if [[ ! -S "/tmp/totalrecall-proxy.sock" ]]; then
    if command -v "$TOTAL_RECALL_ROOT/scripts/proxy-daemon.sh" >/dev/null 2>&1; then
      echo "Starting TLS proxy for reactive features..."
      "$TOTAL_RECALL_ROOT/scripts/proxy-daemon.sh" start >/dev/null 2>&1
    fi
  fi
}

# Uncomment this line to auto-start the proxy when shell starts:
# ensure_proxy_running

# Debug function (optional)
debug_preexec_flow() {
  echo "🐛 Debug: PREEXEC_DATA length: ${#___PREEXEC_DATA}"
  echo "🐛 Debug: COMMAND_ID: $___COMMAND_ID"
  if [[ -n "$___PREEXEC_DATA" && -n "$___COMMAND_ID" ]]; then
    echo "✅ Preexec flow working correctly"
  else
    echo "❌ Preexec flow has issues"
  fi
}

# Uncomment to debug:
# alias debug-preexec='debug_preexec_flow'
