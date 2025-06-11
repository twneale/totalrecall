# Updated preexec.sh with single Go binary for data collection

function preexec() { 
  # Single subprocess instead of 6-8!
  export ___PREEXEC_PWD="$(pwd)"
  export ___PREEXEC_DATA="$($TOTAL_RECALL_ROOT/bin/preexec-hook "$1")"
}

function precmd () {
  local ___RETURN_CODE=$?
  (lf -remote "send cd $___PREEXEC_PWD; set sortby time; set info time" &)
  
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
      
  unset ___PREEXEC_DATA;
}

# Auto-start proxy if it's not running (optional)
ensure_proxy_running() {
  if [[ ! -S "/tmp/totalrecall-proxy.sock" ]]; then
    if command -v "$TOTAL_RECALL_ROOT/scripts/proxy-daemon.sh" >/dev/null 2>&1; then
      echo "Starting TLS proxy..."
      "$TOTAL_RECALL_ROOT/scripts/proxy-daemon.sh" start >/dev/null 2>&1
    fi
  fi
}

# Uncomment this line to auto-start the proxy when shell starts:
# ensure_proxy_running
