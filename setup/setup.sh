#!/bin/bash

# Setup script to automatically configure Elasticsearch and Kibana for Total Recall

set -e

ELASTICSEARCH_URL="${ELASTICSEARCH_URL:-http://elasticsearch:9200}"
KIBANA_URL="${KIBANA_URL:-http://kibana:5601}"
MAX_RETRIES=60
RETRY_DELAY=5

echo "Setting up Total Recall Elasticsearch and Kibana configuration..."

# Function to wait for service to be ready
wait_for_service() {
    local url=$1
    local service_name=$2
    local retries=0
    
    echo "Waiting for $service_name to be ready at $url..."
    
    while [ $retries -lt $MAX_RETRIES ]; do
        if curl -s -f "$url" >/dev/null 2>&1; then
            echo "$service_name is ready!"
            return 0
        fi
        
        echo "Waiting for $service_name... (attempt $((retries + 1))/$MAX_RETRIES)"
        sleep $RETRY_DELAY
        retries=$((retries + 1))
    done
    
    echo "ERROR: $service_name failed to become ready after $((MAX_RETRIES * RETRY_DELAY)) seconds"
    return 1
}

# Wait for Elasticsearch
wait_for_service "$ELASTICSEARCH_URL" "Elasticsearch"

# Create the index template
echo "Creating Elasticsearch index template..."
curl -X PUT "$ELASTICSEARCH_URL/_index_template/totalrecall" \
    -H "Content-Type: application/json" \
    -d @/setup/elasticsearch-template.json

if [ $? -eq 0 ]; then
    echo "✓ Elasticsearch index template created successfully"
else
    echo "✗ Failed to create Elasticsearch index template"
    exit 1
fi

# Wait for Kibana
wait_for_service "$KIBANA_URL/api/status" "Kibana"

# Additional wait for Kibana to fully initialize
echo "Waiting for Kibana to fully initialize..."
sleep 10

# Import Kibana index pattern
echo "Importing Kibana index pattern..."
curl -X POST "$KIBANA_URL/api/saved_objects/_import" \
    -H "kbn-xsrf: true" \
    -H "Content-Type: application/json" \
    -d @/setup/kibana-index-pattern.json

if [ $? -eq 0 ]; then
    echo "✓ Kibana index pattern imported successfully"
else
    echo "✗ Failed to import Kibana index pattern"
    # Don't exit on this failure as the template is more critical
fi

# Create a simple dashboard
echo "Creating default dashboard..."
curl -X POST "$KIBANA_URL/api/saved_objects/_import" \
    -H "kbn-xsrf: true" \
    -H "Content-Type: application/json" \
    -d '{
  "version": "8.0.0",
  "objects": [
    {
      "id": "totalrecall-dashboard",
      "type": "dashboard",
      "attributes": {
        "title": "Total Recall - Command History",
        "description": "Shell command history and analytics",
        "panelsJSON": "[]",
        "timeRestore": false,
        "version": 1
      },
      "references": [
        {
          "id": "totalrecall-*",
          "type": "index-pattern",
          "name": "kibanaSavedObjectMeta.searchSourceJSON.index"
        }
      ]
    }
  ]
}'

echo ""
echo "🎉 Total Recall setup completed!"
echo ""
echo "You can now:"
echo "  1. View command history in Kibana at: http://localhost:8443"
echo "  2. Use the 'totalrecall*' index pattern"
echo "  3. Filter by pwd, env.NODE_ENV, return_code, etc."
echo ""
echo "Useful Kibana queries:"
echo "  - Commands in current directory: pwd.keyword:\"/path/to/directory\""
echo "  - Failed commands: return_code:NOT 0"
echo "  - Environment-specific: env.NODE_ENV.keyword:\"production\""
echo "  - Git commands: command:\"git*\""
echo ""
