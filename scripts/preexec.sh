# see https://github.com/rcaloras/bash-preexec
function preexec() { 
  export ___PREEXEC_PWD="$(pwd)";
  export ___PREEXEC_CMD="$(echo -n $1 | base64)"; 
  export ___PREEXEC_START_TIMESTAMP="$(gdate --rfc-3339=ns)"; 
  
  # Capture environment variables before command execution
  # Filter out the temporary preexec variables and other noise
  export ___PREEXEC_ENV="$(env | grep -v '^___PREEXEC_' | grep -v '^_=' | base64 -w 0)"
}

function precmd () {
  local ___RETURN_CODE=$?
  ($TOTAL_RECALL_ROOT/bin/pwd-updater &)
  (lf -remote "send cd $PWD; set sortby time; set info time" &)
  ($TOTAL_RECALL_ROOT/bin/preexec-hook \
      -command="$___PREEXEC_CMD" \
      -pwd="$___PREEXEC_PWD" \
      -env="$___PREEXEC_ENV" \
      -return-code="$___RETURN_CODE" \
      -start-timestamp="$___PREEXEC_START_TIMESTAMP" -end-timestamp="$(gdate --rfc-3339=ns)" \
      -env-config="$HOME/.totalrecall/env-config.json" \
      --tls \
      --tls-ca-file="$HOME/.totalrecall/ca.crt" \
      --tls-cert-file="$HOME/.totalrecall/client.crt" \
      --tls-key-file="$HOME/.totalrecall/client.key" &);
  unset ___PREEXEC_CMD;
  unset ___PREEXEC_START_TIMESTAMP;
  unset ___PREEXEC_PWD;
  unset ___PREEXEC_ENV;
}
