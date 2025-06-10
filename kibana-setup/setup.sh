#!/bin/sh

set -e

echo "Setting up Total Recall Elasticsearch and Kibana configuration..."

# Wait for Elasticsearch to be ready
echo "Waiting for Elasticsearch to be ready at ${ELASTICSEARCH_URL}..."
until curl -f -s "${ELASTICSEARCH_URL}/_cluster/health" > /dev/null 2>&1; do
    echo "Waiting for Elasticsearch..."
    sleep 2
done
echo "Elasticsearch is ready!"

# Create Elasticsearch index template
echo "Creating Elasticsearch index template..."
echo "Template file contents (first 5 lines):"
cat /setup/index-template.json | head -5

RESPONSE=$(curl -X PUT "${ELASTICSEARCH_URL}/_index_template/totalrecall" \
  -H "Content-Type: application/json" \
  --data-binary @/setup/index-template.json \
  -w "%{http_code}" \
  -s)

HTTP_CODE=$(echo "$RESPONSE" | tail -c 4)
RESPONSE_BODY=$(echo "$RESPONSE" | sed '$d')

echo "HTTP Status: $HTTP_CODE"
if [ "$HTTP_CODE" = "200" ] || [ "$HTTP_CODE" = "201" ]; then
    echo "✓ Elasticsearch index template created successfully"
else
    echo "✗ Failed to create Elasticsearch index template"
    echo "Response: $RESPONSE_BODY"
    exit 1
fi

# Wait for Kibana to be ready
echo "Waiting for Kibana to be ready at ${KIBANA_URL}/api/status..."
until curl -f -s "${KIBANA_URL}/api/status" > /dev/null 2>&1; do
    echo "Waiting for Kibana..."
    sleep 2
done
echo "Kibana is ready!"

# Give Kibana a bit more time to fully initialize
echo "Waiting for Kibana to fully initialize..."
sleep 10

# Import Kibana saved objects (index pattern and dashboard)
echo "Importing Kibana saved objects..."

# Try NDJSON format first (most reliable)
echo "Attempting NDJSON import..."
KIBANA_RESPONSE=$(curl -X POST "${KIBANA_URL}/api/saved_objects/_import" \
  -H "Content-Type: application/ndjson" \
  -H "kbn-xsrf: true" \
  --data-binary @/setup/kibana-objects.ndjson \
  -w "%{http_code}" \
  -s)

KIBANA_HTTP_CODE=$(echo "$KIBANA_RESPONSE" | tail -c 4)
KIBANA_RESPONSE_BODY=$(echo "$KIBANA_RESPONSE" | sed '$d')

echo "Kibana NDJSON import HTTP Status: $KIBANA_HTTP_CODE"
if [ "$KIBANA_HTTP_CODE" = "200" ] || [ "$KIBANA_HTTP_CODE" = "201" ]; then
    echo "✓ Kibana saved objects imported successfully via NDJSON"
    echo "Response: $KIBANA_RESPONSE_BODY"
else
    echo "✗ NDJSON import failed"
    echo "Response: $KIBANA_RESPONSE_BODY"
    
    # Fallback: try direct JSON import 
    echo "Trying fallback JSON import..."
    KIBANA_RESPONSE2=$(curl -X POST "${KIBANA_URL}/api/saved_objects/_import" \
      -H "Content-Type: application/json" \
      -H "kbn-xsrf: true" \
      --data-binary @/setup/kibana-objects.json \
      -w "%{http_code}" \
      -s)
    
    KIBANA_HTTP_CODE=$(echo "$KIBANA_RESPONSE2" | tail -c 4)
    echo "Kibana JSON import HTTP Status: $KIBANA_HTTP_CODE"
    
    if [ "$KIBANA_HTTP_CODE" != "200" ] && [ "$KIBANA_HTTP_CODE" != "201" ]; then
        # Final fallback: manual index pattern creation
        echo "Attempting to create index pattern manually..."
        
        PATTERN_RESPONSE=$(curl -X POST "${KIBANA_URL}/api/saved_objects/index-pattern" \
          -H "Content-Type: application/json" \
          -H "kbn-xsrf: true" \
          -d '{
            "attributes": {
              "title": "totalrecall*",
              "timeFieldName": "start_timestamp"
            }
          }' \
          -w "%{http_code}" \
          -s)
        
        PATTERN_HTTP_CODE=$(echo "$PATTERN_RESPONSE" | tail -c 4)
        echo "Manual index pattern creation HTTP Status: $PATTERN_HTTP_CODE"
        
        if [ "$PATTERN_HTTP_CODE" = "200" ] || [ "$PATTERN_HTTP_CODE" = "201" ]; then
            echo "✓ Index pattern created manually"
            
            # Also try to create a basic dashboard manually
            echo "Creating basic dashboard manually..."
            DASHBOARD_RESPONSE=$(curl -X POST "${KIBANA_URL}/api/saved_objects/dashboard" \
              -H "Content-Type: application/json" \
              -H "kbn-xsrf: true" \
              -d '{
                "attributes": {
                  "title": "Total Recall - Command History",
                  "hits": 0,
                  "description": "Shell command history overview",
                  "panelsJSON": "[]",
                  "optionsJSON": "{\"useMargins\":true,\"syncColors\":false,\"hidePanelTitles\":false}",
                  "version": 1,
                  "timeRestore": true,
                  "timeTo": "now",
                  "timeFrom": "now-24h",
                  "kibanaSavedObjectMeta": {
                    "searchSourceJSON": "{\"query\":{\"query\":\"\",\"language\":\"kuery\"},\"filter\":[]}"
                  }
                }
              }' \
              -w "%{http_code}" \
              -s)
            
            DASHBOARD_HTTP_CODE=$(echo "$DASHBOARD_RESPONSE" | tail -c 4)
            echo "Manual dashboard creation HTTP Status: $DASHBOARD_HTTP_CODE"
            
            if [ "$DASHBOARD_HTTP_CODE" = "200" ] || [ "$DASHBOARD_HTTP_CODE" = "201" ]; then
                echo "✓ Basic dashboard created manually"
                echo "You can add visualizations to it in the Kibana UI"
            else
                echo "✗ Dashboard creation failed, but index pattern is working"
                echo "You can create dashboards manually in Kibana"
            fi
        else
            echo "✗ All Kibana setup methods failed"
            echo "Manual setup required:"
            echo "- Go to http://localhost:8443"
            echo "- Navigate to Stack Management > Index Patterns" 
            echo "- Create new pattern with: totalrecall*"
            echo "- Set time field to: start_timestamp"
        fi
    else
        echo "✓ Kibana objects imported via JSON fallback"
    fi
fi

echo ""
echo "🎉 Total Recall setup completed!"
echo ""
echo "You can now:"
echo "  1. View command history in Kibana at: http://localhost:8443"
echo "  2. Use the 'totalrecall*' index pattern"
echo "  3. Check out the 'Total Recall - Command History' dashboard"
echo "  4. Filter by pwd, env.NODE_ENV, return_code, etc."
echo ""
echo "Useful Kibana queries:"
echo "  - Commands in current directory: pwd:\"/path/to/directory\""
echo "  - Failed commands: return_code:NOT 0"
echo "  - Environment-specific: env.NODE_ENV:\"production\""
echo "  - Git commands: command:\"git*\""
echo "  - Recent commands: start_timestamp:[now-1h TO now]"
echo ""cho ""
