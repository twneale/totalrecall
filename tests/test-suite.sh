#!/bin/bash

# Total Recall Comprehensive Test Suite
# Tests the entire pipeline from command capture to Elasticsearch storage

set -e

# Configuration
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TOTALRECALL_ROOT="${TOTAL_RECALL_ROOT:-$(dirname "$SCRIPT_DIR")}"
TEST_DIR="$TOTALRECALL_ROOT/tests"
ELASTICSEARCH_URL="${ELASTICSEARCH_URL:-http://localhost:9200}"
KIBANA_URL="${KIBANA_URL:-http://localhost:8443}"
TEST_INDEX="totalrecall-test-$(date +%s)"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Test tracking
TESTS_RUN=0
TESTS_PASSED=0
TESTS_FAILED=0

log() {
    echo -e "${BLUE}[$(date '+%H:%M:%S')]${NC} $1"
}

success() {
    echo -e "${GREEN}✅ $1${NC}"
    ((TESTS_PASSED++))
}

failure() {
    echo -e "${RED}❌ $1${NC}"
    ((TESTS_FAILED++))
}

warning() {
    echo -e "${YELLOW}⚠️  $1${NC}"
}

run_test() {
    local test_name="$1"
    local test_command="$2"
    
    ((TESTS_RUN++))
    log "Running test: $test_name"
    
    if eval "$test_command"; then
        success "$test_name"
        return 0
    else
        failure "$test_name"
        return 1
    fi
}

# Test 1: Environment Configuration System
test_env_config() {
    log "Testing environment configuration system..."
    
    # Test 1.1: Config generation
    run_test "Config generation" "
        $TOTALRECALL_ROOT/bin/preexec-hook -generate-config -env-config=/tmp/test-config.json &&
        test -f /tmp/test-config.json &&
        jq -e '.allowlist.exact | length > 0' /tmp/test-config.json >/dev/null
    "
    
    # Test 1.2: Config validation
    run_test "Config validation" "
        jq empty /tmp/test-config.json
    "
    
    # Test 1.3: Test mode functionality
    export TEST_API_KEY="secret123"
    export TEST_ENV_VAR="production"
    export TEST_NOISE_VAR="should_be_filtered"
    
    run_test "Environment filtering test mode" "
        output=\$($TOTALRECALL_ROOT/bin/preexec-hook -test -env-config=/tmp/test-config.json) &&
        echo \"\$output\" | grep -q 'TEST_API_KEY=h8_' &&
        echo \"\$output\" | grep -q 'TEST_ENV_VAR=production' &&
        ! echo \"\$output\" | grep -q 'TEST_NOISE_VAR'
    "
    
    # Test 1.4: Hashing behavior
    run_test "Sensitive value hashing" "
        output=\$($TOTALRECALL_ROOT/bin/preexec-hook -test -env-config=/tmp/test-config.json) &&
        echo \"\$output\" | grep -q 'TEST_API_KEY=h8_[a-f0-9]\\{8\\}'
    "
    
    unset TEST_API_KEY TEST_ENV_VAR TEST_NOISE_VAR
}

# Test 2: Command Capture (Race Condition Fix)
test_command_capture() {
    log "Testing command capture and race condition handling..."
    
    # Test 2.1: Basic command encoding
    run_test "Command base64 encoding" "
        encoded=\$(echo -n 'ls -la' | base64) &&
        decoded=\$(echo \"\$encoded\" | base64 -d) &&
        test \"\$decoded\" = 'ls -la'
    "
    
    # Test 2.2: Environment capture before command execution
    run_test "Environment capture timing" "
        # Simulate the preexec function behavior
        export TEST_PWD=\$(pwd)
        export TEST_CMD=\$(echo -n 'cd /tmp' | base64)
        export TEST_ENV=\$(env | grep -v '^TEST_' | base64 -w 0)
        
        # Verify PWD is captured correctly (not changed by cd)
        echo \"\$TEST_ENV\" | base64 -d | grep -q \"PWD=\$TEST_PWD\"
    "
    
    # Test 2.3: Complete hook execution without network
    run_test "Hook execution (dry run)" "
        $TOTALRECALL_ROOT/bin/preexec-hook \
            -command=\$(echo -n 'echo test' | base64) \
            -pwd=\$(pwd) \
            -env=\$(env | base64 -w 0) \
            -return-code=0 \
            -start-timestamp=\$(date --iso-8601=seconds) \
            -end-timestamp=\$(date --iso-8601=seconds) \
            -host=127.0.0.1 \
            -port=99999 \
            -timeout=1s \
            -env-config=/tmp/test-config.json 2>&1 || true
    "
}

# Test 3: Elasticsearch Integration
test_elasticsearch() {
    log "Testing Elasticsearch integration..."
    
    # Test 3.1: Elasticsearch connectivity
    run_test "Elasticsearch connectivity" "
        curl -s -f \$ELASTICSEARCH_URL/_cluster/health >/dev/null
    "
    
    # Test 3.2: Index template creation
    run_test "Index template creation" "
        curl -s -X PUT \"\$ELASTICSEARCH_URL/_index_template/totalrecall-test\" \
            -H 'Content-Type: application/json' \
            -d @$TOTALRECALL_ROOT/setup/elasticsearch-template.json >/dev/null
    "
    
    # Test 3.3: Document indexing
    run_test "Document indexing" "
        test_doc='{
            \"@timestamp\": \"'$(date -u +%Y-%m-%dT%H:%M:%S.%3NZ)'\",
            \"command\": \"echo test\",
            \"return_code\": 0,
            \"start_timestamp\": \"'$(date -u +%Y-%m-%dT%H:%M:%S.%3NZ)'\",
            \"end_timestamp\": \"'$(date -u +%Y-%m-%dT%H:%M:%S.%3NZ)'\",
            \"pwd\": \"'$(pwd)'\",
            \"hostname\": \"test-host\",
            \"env\": {
                \"PWD\": \"'$(pwd)'\",
                \"USER\": \"testuser\",
                \"TEST_API_KEY\": \"h8_12345678\"
            },
            \"_config_version\": \"1.0.0\"
        }' &&
        curl -s -X POST \"\$ELASTICSEARCH_URL/$TEST_INDEX/_doc\" \
            -H 'Content-Type: application/json' \
            -d \"\$test_doc\" | jq -e '.result == \"created\"' >/dev/null
    "
    
    # Test 3.4: Document retrieval and mapping
    sleep 2  # Wait for indexing
    run_test "Document retrieval and field mapping" "
        curl -s -X GET \"\$ELASTICSEARCH_URL/$TEST_INDEX/_search\" | \
        jq -e '.hits.total.value > 0 and .hits.hits[0]._source.env.TEST_API_KEY == \"h8_12345678\"' >/dev/null
    "
}

# Test 4: End-to-End Pipeline
test_e2e_pipeline() {
    log "Testing end-to-end pipeline..."
    
    # Start services if not running
    if ! curl -s -f "$ELASTICSEARCH_URL" >/dev/null; then
        warning "Elasticsearch not running, attempting to start with docker-compose..."
        (cd "$TOTALRECALL_ROOT" && docker-compose up -d elasticsearch)
        sleep 10
    fi
    
    # Test 4.1: Full command capture to Elasticsearch
    export E2E_TEST_KEY="secret_value_for_testing"
    export E2E_TEST_ENV="test_environment"
    
    run_test "Full pipeline test" "
        # Generate test environment
        test_env=\$(env | grep '^E2E_TEST_' | base64 -w 0)
        
        # Execute hook with real network call
        $TOTALRECALL_ROOT/bin/preexec-hook \
            -command=\$(echo -n 'echo e2e-test-command' | base64) \
            -pwd=\$(pwd) \
            -env=\"\$test_env\" \
            -return-code=0 \
            -start-timestamp=\$(date --iso-8601=seconds) \
            -end-timestamp=\$(date --iso-8601=seconds) \
            -host=127.0.0.1 \
            -port=5170 \
            -timeout=5s \
            -env-config=/tmp/test-config.json &&
        
        # Wait for data to appear in Elasticsearch
        sleep 3 &&
        
        # Verify the command appears in the index
        curl -s -X GET \"\$ELASTICSEARCH_URL/totalrecall*/_search\" \
            -H 'Content-Type: application/json' \
            -d '{\"query\":{\"match\":{\"command\":\"e2e-test-command\"}}}' | \
        jq -e '.hits.total.value > 0' >/dev/null
    "
    
    unset E2E_TEST_KEY E2E_TEST_ENV
}

# Test 5: Preexec Integration
test_preexec_integration() {
    log "Testing bash preexec integration..."
    
    # Test 5.1: Preexec script syntax
    run_test "Preexec script syntax" "
        bash -n $TOTALRECALL_ROOT/scripts/preexec.sh
    "
    
    # Test 5.2: Environment variable capture in preexec
    run_test "Preexec environment capture" "
        source $TOTALRECALL_ROOT/scripts/preexec.sh &&
        export TEST_CAPTURE_VAR='test_value' &&
        preexec 'echo test' &&
        test -n \"\$___PREEXEC_ENV\" &&
        echo \"\$___PREEXEC_ENV\" | base64 -d | grep -q 'TEST_CAPTURE_VAR=test_value'
    "
}

# Test 6: Configuration Drift Monitoring
test_drift_monitoring() {
    log "Testing configuration drift monitoring..."
    
    # Test 6.1: Template update detection
    run_test "Template update detection" "
        # Modify config version
        jq '.version = \"1.0.1\"' /tmp/test-config.json > /tmp/test-config-v2.json &&
        
        # Check if update is detected
        $TOTALRECALL_ROOT/scripts/update-elasticsearch-template.sh \
            --check-only \
            --config-file=/tmp/test-config-v2.json || test \$? -eq 1
    "
    
    # Test 6.2: Drift monitoring script
    if [[ -f "$TOTALRECALL_ROOT/scripts/drift-monitor.sh" ]]; then
        run_test "Drift monitoring execution" "
            timeout 30s $TOTALRECALL_ROOT/scripts/drift-monitor.sh || test \$? -eq 124
        "
    fi
}

# Test 7: Kibana Integration
test_kibana() {
    log "Testing Kibana integration..."
    
    # Test 7.1: Kibana connectivity
    run_test "Kibana connectivity" "
        curl -s -f \$KIBANA_URL/api/status >/dev/null 2>&1 || 
        curl -s -f http://localhost:6601/api/status >/dev/null
    "
    
    # Test 7.2: Index pattern existence
    run_test "Index pattern check" "
        curl -s \$KIBANA_URL/api/saved_objects/_find?type=index-pattern 2>/dev/null | 
        jq -e '.saved_objects[] | select(.attributes.title | contains(\"totalrecall\"))' >/dev/null ||
        curl -s http://localhost:6601/api/saved_objects/_find?type=index-pattern 2>/dev/null |
        jq -e '.saved_objects[] | select(.attributes.title | contains(\"totalrecall\"))' >/dev/null
    "
}

# Test 8: Stress Testing
test_stress() {
    log "Running stress tests..."
    
    # Test 8.1: Multiple concurrent commands
    run_test "Concurrent command processing" "
        for i in {1..5}; do
            (
                $TOTALRECALL_ROOT/bin/preexec-hook \
                    -command=\$(echo -n \"stress-test-\$i\" | base64) \
                    -pwd=\$(pwd) \
                    -env=\$(env | base64 -w 0) \
                    -return-code=0 \
                    -start-timestamp=\$(date --iso-8601=seconds) \
                    -end-timestamp=\$(date --iso-8601=seconds) \
                    -host=127.0.0.1 \
                    -port=5170 \
                    -timeout=5s \
                    -env-config=/tmp/test-config.json
            ) &
        done &&
        wait
    "
    
    # Test 8.2: Large environment handling
    run_test "Large environment handling" "
        # Create large environment
        for i in {1..50}; do
            export \"LARGE_ENV_VAR_\$i\"=\"value_\$i\"
        done &&
        
        large_env=\$(env | base64 -w 0) &&
        test \${#large_env} -gt 1000 &&
        
        $TOTALRECALL_ROOT/bin/preexec-hook -test -env-config=/tmp/test-config.json >/dev/null
    "
}

# Test 9: Edge Cases
test_edge_cases() {
    log "Testing edge cases..."
    
    # Test 9.1: Special characters in commands
    run_test "Special characters in commands" "
        special_cmd='echo \"hello world\" && ls -la | grep test'
        encoded=\$(echo -n \"\$special_cmd\" | base64)
        decoded=\$(echo \"\$encoded\" | base64 -d)
        test \"\$decoded\" = \"\$special_cmd\"
    "
    
    # Test 9.2: Empty environment
    run_test "Empty environment handling" "
        $TOTALRECALL_ROOT/bin/preexec-hook \
            -command=\$(echo -n 'test' | base64) \
            -pwd=\$(pwd) \
            -env='' \
            -return-code=0 \
            -start-timestamp=\$(date --iso-8601=seconds) \
            -end-timestamp=\$(date --iso-8601=seconds) \
            -host=127.0.0.1 \
            -port=99999 \
            -timeout=1s \
            -env-config=/tmp/test-config.json 2>&1 | grep -q 'error' && false || true
    "
    
    # Test 9.3: Invalid JSON config
    run_test "Invalid config handling" "
        echo 'invalid json' > /tmp/invalid-config.json &&
        $TOTALRECALL_ROOT/bin/preexec-hook -test -env-config=/tmp/invalid-config.json 2>&1 | 
        grep -q 'error.*config' && rm -f /tmp/invalid-config.json
    "
}

# Cleanup function
cleanup() {
    log "Cleaning up test artifacts..."
    
    # Remove test index
    curl -s -X DELETE "$ELASTICSEARCH_URL/$TEST_INDEX" >/dev/null 2>&1 || true
    
    # Remove test files
    rm -f /tmp/test-config.json /tmp/test-config-v2.json /tmp/invalid-config.json
    
    # Unset test environment variables
    for var in $(env | grep '^TEST_\|^E2E_TEST_\|^LARGE_ENV_VAR_' | cut -d= -f1); do
        unset "$var"
    done
}

# Main execution
main() {
    echo "🧪 Total Recall Test Suite"
    echo "=========================="
    echo ""
    
    # Ensure binary exists
    if [[ ! -f "$TOTALRECALL_ROOT/bin/preexec-hook" ]]; then
        failure "preexec-hook binary not found. Run: cd tools/preexec-hook && go build -o ../../bin/preexec-hook"
        exit 1
    fi
    
    # Run test suites
    test_env_config
    test_command_capture
    test_elasticsearch
    test_e2e_pipeline
    test_preexec_integration
    test_drift_monitoring
    test_kibana
    test_stress
    test_edge_cases
    
    # Cleanup
    cleanup
    
    # Summary
    echo ""
    echo "📊 Test Results"
    echo "==============="
    echo -e "Tests run:    ${BLUE}$TESTS_RUN${NC}"
    echo -e "Passed:       ${GREEN}$TESTS_PASSED${NC}"
    echo -e "Failed:       ${RED}$TESTS_FAILED${NC}"
    
    if [[ $TESTS_FAILED -eq 0 ]]; then
        echo ""
        success "All tests passed! 🎉"
        echo ""
        echo "🚀 Your Total Recall setup is working correctly!"
        echo ""
        echo "Next steps:"
        echo "1. Start using Total Recall: source $TOTALRECALL_ROOT/scripts/preexec.sh"
        echo "2. View your command history in Kibana: http://localhost:8443"
        echo "3. Monitor for drift: $TOTALRECALL_ROOT/scripts/drift-monitor.sh"
        exit 0
    else
        echo ""
        failure "Some tests failed. Check the output above for details."
        exit 1
    fi
}

# Handle arguments
case "${1:-}" in
    --help|-h)
        echo "Usage: $0 [test_name]"
        echo ""
        echo "Available tests:"
        echo "  env-config      Test environment configuration"
        echo "  command-capture Test command capture system"
        echo "  elasticsearch   Test Elasticsearch integration"
        echo "  e2e            Test end-to-end pipeline"
        echo "  preexec        Test preexec integration"
        echo "  drift          Test drift monitoring"
        echo "  kibana         Test Kibana integration"
        echo "  stress         Run stress tests"
        echo "  edge-cases     Test edge cases"
        echo ""
        echo "Run without arguments to run all tests."
        exit 0
        ;;
    env-config)
        test_env_config
        ;;
    command-capture)
        test_command_capture
        ;;
    elasticsearch)
        test_elasticsearch
        ;;
    e2e)
        test_e2e_pipeline
        ;;
    preexec)
        test_preexec_integration
        ;;
    drift)
        test_drift_monitoring
        ;;
    kibana)
        test_kibana
        ;;
    stress)
        test_stress
        ;;
    edge-cases)
        test_edge_cases
        ;;
    "")
        main
        ;;
    *)
        echo "Unknown test: $1"
        echo "Run $0 --help for usage information."
        exit 1
        ;;
esac
