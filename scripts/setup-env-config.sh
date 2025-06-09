#!/bin/bash

# Script to help users set up and manage their Total Recall environment configuration

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TOTALRECALL_ROOT="${TOTAL_RECALL_ROOT:-$(dirname "$SCRIPT_DIR")}"
CONFIG_DIR="$HOME/.totalrecall"
CONFIG_FILE="$CONFIG_DIR/env-config.json"

usage() {
    cat << EOF
Usage: $0 [command]

Commands:
    generate    Generate default environment configuration
    validate    Validate existing configuration
    test        Test current environment against configuration
    edit        Open configuration in default editor
    show        Show current configuration
    reset       Reset to default configuration

Environment Variables:
    TOTAL_RECALL_ROOT    Path to Total Recall installation (default: auto-detect)
    EDITOR              Editor to use for 'edit' command (default: vi)

Examples:
    $0 generate          # Create initial configuration
    $0 test              # See which env vars would be captured
    $0 edit              # Edit the configuration
EOF
}

ensure_binary() {
    if [[ ! -f "$TOTALRECALL_ROOT/bin/preexec-hook" ]]; then
        echo "Error: preexec-hook binary not found at $TOTALRECALL_ROOT/bin/preexec-hook"
        echo "Please build the binary first: cd $TOTALRECALL_ROOT/tools/preexec-hook && go build -o ../../bin/preexec-hook"
        exit 1
    fi
}

generate_config() {
    echo "Generating default environment configuration..."
    ensure_binary
    
    mkdir -p "$CONFIG_DIR"
    
    "$TOTALRECALL_ROOT/bin/preexec-hook" -generate-config -env-config="$CONFIG_FILE"
    
    echo "Configuration generated at: $CONFIG_FILE"
    echo ""
    echo "You can now:"
    echo "  1. Edit the configuration: $0 edit"
    echo "  2. Test the configuration: $0 test"
    echo "  3. View current settings: $0 show"
}

validate_config() {
    if [[ ! -f "$CONFIG_FILE" ]]; then
        echo "Error: Configuration file not found at $CONFIG_FILE"
        echo "Run '$0 generate' to create it."
        exit 1
    fi
    
    if ! jq empty "$CONFIG_FILE" 2>/dev/null; then
        echo "Error: Configuration file is not valid JSON"
        echo "Run '$0 reset' to restore defaults, or edit manually"
        exit 1
    fi
    
    echo "Configuration file is valid JSON"
}

test_config() {
    validate_config
    ensure_binary
    
    echo "Testing current environment against configuration..."
    echo ""
    
    "$TOTALRECALL_ROOT/bin/preexec-hook" -test -env-config="$CONFIG_FILE"
    
    echo ""
    echo "Configuration test complete."
    echo "To modify what gets captured, edit: $CONFIG_FILE"
}

edit_config() {
    validate_config
    
    "${EDITOR:-vi}" "$CONFIG_FILE"
    
    echo "Configuration updated. Run '$0 validate' to check syntax."
}

show_config() {
    validate_config
    
    echo "Current environment configuration:"
    echo ""
    
    if command -v jq >/dev/null 2>&1; then
        jq . "$CONFIG_FILE"
    else
        cat "$CONFIG_FILE"
    fi
}

reset_config() {
    echo "Resetting configuration to defaults..."
    
    if [[ -f "$CONFIG_FILE" ]]; then
        backup="$CONFIG_FILE.backup.$(date +%Y%m%d_%H%M%S)"
        cp "$CONFIG_FILE" "$backup"
        echo "Backed up existing configuration to: $backup"
    fi
    
    generate_config
}

case "${1:-}" in
    generate)
        generate_config
        ;;
    validate)
        validate_config
        echo "Configuration is valid"
        ;;
    test)
        test_config
        ;;
    edit)
        edit_config
        ;;
    show)
        show_config
        ;;
    reset)
        reset_config
        ;;
    help|--help|-h)
        usage
        ;;
    *)
        if [[ -n "${1:-}" ]]; then
            echo "Error: Unknown command '$1'"
            echo ""
        fi
        usage
        exit 1
        ;;
esac
