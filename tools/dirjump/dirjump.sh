#!/bin/bash
# dirjump.sh - Wrapper to execute dirjump and change directory

# Run the dirjump program and capture stderr
# stderr will contain the temporary script path to source
result=$(~/c/totalrecall/tools/dirjump/dirjump)

# If result is not empty and the file exists, source it to change directory
if [ -n "$result" ] && [ -f "$result" ]; then
    source "$result"
    rm "$result"  # Clean up the temporary file
fi
