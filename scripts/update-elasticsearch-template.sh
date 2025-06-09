#!/bin/bash

# Auto-update Elasticsearch template when env config changes

set -e

ELASTICSEARCH_URL="${ELASTICSEARCH_URL:-http://localhost:9200}"
CONFIG_FILE="${CONFIG_FILE:-$HOME/.totalrecall/env-config.json}"
TEMPLATE_NAME="totalrecall"
BACKUP_DIR="$HOME/.totalrecall/backups"

usage() {
    cat << EOF
Usage: $0 [options]

Options:
    --check-only    Only check if update is needed, don't apply changes
    --force         Force update even if versions match
    --backup        Create backup before updating
    --dry-run       Show what would be updated without applying
    
Environment Variables:
    ELASTICSEARCH_URL   Elasticsearch URL (default: http://localhost:9200)
    CONFIG_FILE         Path to env config file
    
Examples:
    $0                          # Update template if config changed
    $0 --check-only            # Just check if update needed
    $0 --force --backup        # Force update with backup
EOF
}

# Parse arguments
CHECK_ONLY=false
FORCE_UPDATE=false
CREATE_BACKUP=false
DRY_RUN=false

while [[ $# -gt 0 ]]; do
    case $1 in
        --check-only)
            CHECK_ONLY=true
            shift
            ;;
        --force)
            FORCE_UPDATE=true
            shift
            ;;
        --backup)
            CREATE_BACKUP=true
            shift
            ;;
        --dry-run)
            DRY_RUN=true
            shift
            ;;
        --help|-h)
            usage
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            usage
            exit 1
            ;;
    esac
done

echo "🔧 Total Recall Elasticsearch Template Updater"
echo ""

# Check if config file exists
if [[ ! -f "$CONFIG_FILE" ]]; then
    echo "❌ Config file not found: $CONFIG_FILE"
    echo "Run: ./scripts/setup-env-config.sh generate"
    exit 1
fi

# Get current config version
CONFIG_VERSION=$(jq -r '.version // "unknown"' "$CONFIG_FILE")
echo "📋 Current config version: $CONFIG_VERSION"

# Get current template version
CURRENT_TEMPLATE=$(curl -s "$ELASTICSEARCH_URL/_index_template/$TEMPLATE_NAME" 2>/dev/null)

if echo "$CURRENT_TEMPLATE" | jq -e '.index_templates[0]' >/dev/null 2>&1; then
    TEMPLATE_VERSION=$(echo "$CURRENT_TEMPLATE" | jq -r '.index_templates[0].index_template._meta.config_version // "unknown"')
    echo "🗄️  Current template version: $TEMPLATE_VERSION"
else
    TEMPLATE_VERSION="none"
    echo "🗄️  No existing template found"
fi

# Check if update is needed
UPDATE_NEEDED=false
if [[ "$FORCE_UPDATE" == "true" ]]; then
    UPDATE_NEEDED=true
    echo "🔄 Force update requested"
elif [[ "$TEMPLATE_VERSION" != "$CONFIG_VERSION" ]]; then
    UPDATE_NEEDED=true
    echo "🔄 Template update needed: $TEMPLATE_VERSION → $CONFIG_VERSION"
else
    echo "✅ Template is up to date"
fi

if [[ "$CHECK_ONLY" == "true" ]]; then
    if [[ "$UPDATE_NEEDED" == "true" ]]; then
        echo "📊 Update would be applied"
        exit 1
    else
        echo "📊 No update needed"
        exit 0
    fi
fi

if [[ "$UPDATE_NEEDED" == "false" ]]; then
    echo "✅ No update needed"
    exit 0
fi

# Generate new template from config
echo "🔨 Generating new template from config..."

# Create backup if requested
if [[ "$CREATE_BACKUP" == "true" && "$TEMPLATE_VERSION" != "none" ]]; then
    echo "💾 Creating backup..."
    mkdir -p "$BACKUP_DIR"
    BACKUP_FILE="$BACKUP_DIR/template-backup-$(date +%Y%m%d-%H%M%S).json"
    echo "$CURRENT_TEMPLATE" > "$BACKUP_FILE"
    echo "💾 Backup saved: $BACKUP_FILE"
fi

# Generate allowlist-specific field mappings
generate_field_mappings() {
    local config_file=$1
    
    # Extract exact allowlist fields
    jq -r '.allowlist.exact[]' "$config_file" | while read -r field; do
        case "$field" in
            *_URL|*_DSN|*_ENDPOINT)
                echo "\"$field\": {\"type\": \"keyword\", \"ignore_above\": 512, \"index\": false},"
                ;;
            *_PATH|*_HOME|*_ROOT|PWD|OLDPWD)
                echo "\"$field\": {\"type\": \"keyword\", \"ignore_above\": 512},"
                ;;
            *_ENV|*_ENVIRONMENT|*_STAGE)
                echo "\"$field\": {\"type\": \"keyword\", \"ignore_above\": 32},"
                ;;
            *)
                echo "\"$field\": {\"type\": \"keyword\", \"ignore_above\": 256},"
                ;;
        esac
    done
}

# Create new template
create_template() {
    local version=$1
    
    cat > /tmp/totalrecall-template.json << EOF
{
  "index_patterns": ["totalrecall*"],
  "template": {
    "settings": {
      "number_of_shards": 1,
      "number_of_replicas": 0,
      "index.refresh_interval": "5s",
      "index.mapping.ignore_malformed": true,
      "index.mapping.total_fields.limit": 2000
    },
    "mappings": {
      "dynamic": "true",
      "dynamic_templates": [
        {
          "env_hashed_values": {
            "path_match": "env.*",
            "match_pattern": "regex",
            "match": "^h8_[a-f0-9]{8}$",
            "mapping": {
              "type": "keyword",
              "ignore_above": 32,
              "index": true,
              "doc_values": true
            }
          }
        },
        {
          "env_paths": {
            "path_match": "env.*",
            "match_pattern": "regex", 
            "match": "^(/[^\\\\s]*|[A-Z]:\\\\\\\\[^\\\\s]*)$",
            "mapping": {
              "type": "keyword",
              "ignore_above": 512,
              "index": true,
              "doc_values": true
            }
          }
        },
        {
          "env_urls": {
            "path_match": "env.*",
            "match_pattern": "regex",
            "match": "^https?://.*",
            "mapping": {
              "type": "keyword", 
              "ignore_above": 256,
              "index": false,
              "doc_values": false
            }
          }
        },
        {
          "env_default_keyword": {
            "path_match": "env.*",
            "mapping": {
              "type": "keyword",
              "ignore_above": 256,
              "index": true,
              "doc_values": true
            }
          }
        }
      ],
      "properties": {
        "@timestamp": {"type": "date"},
        "command": {
          "type": "text",
          "fields": {"keyword": {"type": "keyword", "ignore_above": 1024}},
          "analyzer": "standard"
        },
        "return_code": {"type": "integer"},
        "start_timestamp": {"type": "date"},
        "end_timestamp": {"type": "date"},
        "pwd": {"type": "keyword", "ignore_above": 512},
        "hostname": {"type": "keyword", "ignore_above": 256},
        "ip_address": {"type": "ip"},
        "env": {"type": "object", "dynamic": true},
        "_config_version": {"type": "keyword"}
      }
    }
  },
  "priority": 100,
  "version": 3,
  "_meta": {
    "description": "Template for Total Recall shell command history",
    "created_by": "total-recall",
    "config_version": "$version",
    "updated": "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  }
}
EOF
}

# Generate the template
create_template "$CONFIG_VERSION"

if [[ "$DRY_RUN" == "true" ]]; then
    echo "🔍 Dry run - would apply this template:"
    cat /tmp/totalrecall-template.json | jq '.'
    rm -f /tmp/totalrecall-template.json
    exit 0
fi

# Apply the template
echo "🚀 Applying new template..."
RESPONSE=$(curl -s -X PUT "$ELASTICSEARCH_URL/_index_template/$TEMPLATE_NAME" \
    -H "Content-Type: application/json" \
    -d @/tmp/totalrecall-template.json)

if echo "$RESPONSE" | jq -e '.acknowledged' >/dev/null 2>&1; then
    echo "✅ Template updated successfully!"
    echo "📊 New template version: $CONFIG_VERSION"
    
    # Clean up
    rm -f /tmp/totalrecall-template.json
    
    echo ""
    echo "🔄 Next steps:"
    echo "1. New data will use the updated mapping automatically"
    echo "2. Consider reindexing recent data for consistency:"
    echo "   curl -X POST '$ELASTICSEARCH_URL/totalrecall/_reindex'"
    echo "3. Monitor for any mapping conflicts:"
    echo "   ./scripts/drift-monitor.sh"
    
else
    echo "❌ Template update failed!"
    echo "Response: $RESPONSE"
    rm -f /tmp/totalrecall-template.json
    exit 1
fi
