# see https://github.com/rcaloras/bash-preexec
function preexec() { 
  export ___PREEXEC_PWD="$(pwd)";
  export ___PREEXEC_CMD="$(echo -n $1 | base64)"; 
  export ___PREEXEC_START_TIMESTAMP="$(gdate --rfc-3339=ns)"; 
}

function precmd () {
  local ___RETURN_CODE=$?
  ($TOTAL_RECALL_ROOT/bin/pwd-updater &)
  (lf -remote "send cd $PWD; set sortby time; set info time" &)
  ($TOTAL_RECALL_ROOT/bin/preexec-hook \
      -command="$___PREEXEC_CMD" \
      -pwd="$___PREEXEC_PWD" \
      -return-code="$___RETURN_CODE" \
      -start-timestamp="$___PREEXEC_START_TIMESTAMP" -end-timestamp="$(gdate --rfc-3339=ns)" \
      --tls \
      --tls-ca-file="$HOME/.totalrecall/ca.crt" \
      --tls-cert-file="$HOME/.totalrecall/client.crt" \
      --tls-key-file="$HOME/.totalrecall/client.key" &);
  unset ___PREEXEC_CMD;
  unset ___PREEXEC_START_TIMESTAMP;
}

