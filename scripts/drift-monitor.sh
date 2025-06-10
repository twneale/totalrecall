#!/bin/bash

# Monitor for mapping drift and recommend updates

ELASTICSEARCH_URL="${ELASTICSEARCH_URL:-http://localhost:9200}"
INDEX_PATTERN="${INDEX_PATTERN:-totalrecall*}"
ALERT_THRESHOLD=10  # Alert when more than 10 unmapped fields found

echo "🔍 Monitoring mapping drift for Total Recall..."

# Function to get unmapped fields
get_unmapped_fields() {
    curl -s "$ELASTICSEARCH_URL/$INDEX_PATTERN/_mapping" | \
    jq -r '
    [.[].mappings.properties.env.properties // {} | keys[]] as $mapped_fields |
    [.[].mappings.dynamic_templates[]? | select(.path_match == "env.*") | .match // empty] as $patterns |
    {
        "mapped_fields": ($mapped_fields | unique),
        "dynamic_patterns": ($patterns | unique),
        "total_mapped": ($mapped_fields | length)
    }'
}

# Function to get recently used env fields
get_recent_env_fields() {
    curl -s -X POST "$ELASTICSEARCH_URL/$INDEX_PATTERN/_search" \
    -H "Content-Type: application/json" \
    -d '{
        "size": 0,
        "query": {
            "range": {
                "start_timestamp": {
                    "gte": "now-24h"
                }
            }
        },
        "aggs": {
            "env_fields": {
                "nested": {
                    "path": "env"
                },
                "aggs": {
                    "field_names": {
                        "terms": {
                            "field": "_field_names",
                            "include": "env.*",
                            "size": 1000
                        }
                    }
                }
            }
        }
    }' | jq -r '.aggregations.env_fields.field_names.buckets[].key'
}

# Function to detect new hashing patterns
detect_hash_pattern_changes() {
    curl -s -X POST "$ELASTICSEARCH_URL/$INDEX_PATTERN/_search" \
    -H "Content-Type: application/json" \
    -d '{
        "size": 0,
        "query": {
            "range": {
                "start_timestamp": {
                    "gte": "now-7d"
                }
            }
        },
        "aggs": {
            "config_versions": {
                "terms": {
                    "field": "_config_version.keyword",
                    "size": 10
                }
            },
            "hash_patterns": {
                "filter": {
                    "wildcard": {
                        "env.*": "h8_*"
                    }
                }
            }
        }
    }' | jq '.aggregations'
}

# Function to recommend template updates
recommend_updates() {
    local unmapped_count=$1
    local new_fields=$2
    
    if [ "$unmapped_count" -gt "$ALERT_THRESHOLD" ]; then
        echo "⚠️  WARNING: $unmapped_count unmapped environment fields detected!"
        echo ""
        echo "🔧 Recommended actions:"
        echo "1. Review new fields:"
        echo "$new_fields" | head -20
        echo ""
        echo "2. Update your env-config.json to include/exclude new fields"
        echo "3. Run template update: ./scripts/update-elasticsearch-template.sh"
        echo "4. Consider reindexing recent data for consistency"
        echo ""
        
        # Generate suggested config additions
        echo "💡 Suggested allowlist additions:"
        echo "$new_fields" | grep -E "(ENV|PROFILE|KEY|URL|HOST|PORT)$" | head -10
        echo ""
        
        return 1
    else
        echo "✅ Mapping drift within acceptable limits ($unmapped_count fields)"
        return 0
    fi
}

# Function to check field type consistency
check_field_consistency() {
    curl -s -X POST "$ELASTICSEARCH_URL/$INDEX_PATTERN/_search" \
    -H "Content-Type: application/json" \
    -d '{
        "size": 0,
        "aggs": {
            "env_field_types": {
                "scripted_metric": {
                    "init_script": "state.field_types = [:]",
                    "map_script": """
                        if (doc.containsKey(\"env\")) {
                            for (field in doc[\"env\"].keySet()) {
                                def value = doc[\"env\"][field].value;
                                def type = \"unknown\";
                                if (value instanceof String) {
                                    if (value.startsWith(\"h8_\")) {
                                        type = \"hashed\";
                                    } else if (value.matches(\"^/.*\")) {
                                        type = \"path\";
                                    } else if (value.matches(\"^https?://.*\")) {
                                        type = \"url\";
                                    } else {
                                        type = \"string\";
                                    }
                                }
                                
                                if (!state.field_types.containsKey(field)) {
                                    state.field_types[field] = [:];
                                }
                                
                                if (!state.field_types[field].containsKey(type)) {
                                    state.field_types[field][type] = 0;
                                }
                                state.field_types[field][type]++;
                            }
                        }
                    """,
                    "combine_script": "return state.field_types",
                    "reduce_script": """
                        def result = [:];
                        for (state in states) {
                            for (field in state.keySet()) {
                                if (!result.containsKey(field)) {
                                    result[field] = [:];
                                }
                                for (type in state[field].keySet()) {
                                    if (!result[field].containsKey(type)) {
                                        result[field][type] = 0;
                                    }
                                    result[field][type] += state[field][type];
                                }
                            }
                        }
                        return result;
                    """
                }
            }
        }
    }' | jq -r '.aggregations.env_field_types.value'
}

# Main execution
echo "📊 Analyzing field usage patterns..."

# Get current mapping state
mapping_info=$(get_unmapped_fields)
echo "Current mapping state: $mapping_info"

# Get recent field usage
echo "🕐 Checking recent environment field usage..."
recent_fields=$(get_recent_env_fields)
new_field_count=$(echo "$recent_fields" | wc -l)

echo "Found $new_field_count environment fields in use (last 24h)"

# Check for inconsistencies
echo "🔍 Checking field type consistency..."
field_consistency=$(check_field_consistency)

# Detect configuration changes
echo "📋 Checking configuration version changes..."
config_changes=$(detect_hash_pattern_changes)

echo "Configuration analysis:"
echo "$config_changes" | jq '.'

# Generate recommendations
recommend_updates "$new_field_count" "$recent_fields"

if [ $? -ne 0 ]; then
    echo ""
    echo "🚀 To fix mapping issues automatically:"
    echo "   ./scripts/setup-env-config.sh validate"
    echo "   ./scripts/update-elasticsearch-template.sh"
    echo ""
    
    exit 1
fi

echo ""
echo "✅ Total Recall mapping health check complete!"
