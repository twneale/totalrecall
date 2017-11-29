#!/usr/bin/env bash 
#auditreduce -r <(id) -m AUE_EXECVE -m AUE_FEXECVE -m AUE_EXEC /dev/auditpipe | praudit -lx
auditreduce -r <(id) /dev/auditpipe | praudit -lx
