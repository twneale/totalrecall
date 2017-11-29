# see https://github.com/rcaloras/bash-preexec
function preexec() { 
  export ___PREEXEC_CMD="$(echo -n $1 | base64)"; 
  export ___PREEXEC_START_TIMESTAMP="$(date --iso-8601=ns)"; 
}

function precmd () {
  ($TOTAL_RECALL_ROOT/totalrecall_sender.py \
      --command="$___PREEXEC_CMD" \
      --return-code="$?" \
      --start-timestamp="$___PREEXEC_START_TIMESTAMP" --end-timestamp="$(date --iso-8601=ns)" &); 
  unset ___PREEXEC_CMD;
  unset ___PREEXEC_START_TIMESTAMP;
}

