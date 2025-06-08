# Add this to your ~/.bashrc or ~/.bash_profile

# Path to the dirjump.sh wrapper script
export DIRJUMP_WRAPPER="$PWD/dirjump.sh"

# Bind Ctrl+J to run the dirjump wrapper
bind '"\C-j":"source $DIRJUMP_WRAPPER\n"'

# Set Elasticsearch URL if needed (default is http://localhost:9200)
export ES_URL="http://localhost:9200"
