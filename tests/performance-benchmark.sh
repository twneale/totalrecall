#!/bin/bash

# Performance benchmark for Total Recall
# Measures the overhead of command capture on shell performance

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TOTALRECALL_ROOT="${TOTAL_RECALL_ROOT:-$(dirname "$SCRIPT_DIR")}"
ITERATIONS=100
WARMUP_ITERATIONS=10

# Colors
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log() {
    echo -e "${BLUE}[$(date '+%H:%M:%S')]${NC} $1"
}

success() {
    echo -e "${GREEN}✅ $1${NC}"
}

warning() {
    echo -e "${YELLOW}⚠️  $1${NC}"
}

# Benchmark function
benchmark() {
    local test_name="$1"
    local command="$2"
    local iterations="${3:-$ITERATIONS}"
    
    log "Benchmarking: $test_name"
    
    # Warmup
    for ((i=1; i<=WARMUP_ITERATIONS; i++)); do
        eval "$command" >/dev/null 2>&1
    done
    
    # Actual benchmark
    local start_time=$(date +%s.%N)
    for ((i=1; i<=iterations; i++)); do
        eval "$command" >/dev/null 2>&1
    done
    local end_time=$(date +%s.%N)
    
    local total_time=$(echo "$end_time - $start_time" | bc -l)
    local avg_time=$(echo "scale=6; $total_time / $iterations" | bc -l)
    local avg_ms=$(echo "scale=3; $avg_time * 1000" | bc -l)
    
    echo "  Total time: ${total_time}s"
    echo "  Average time: ${avg_ms}ms per execution"
    echo "  Iterations: $iterations"
    echo ""
    
    # Store result for comparison
    echo "$avg_ms" > "/tmp/benchmark_${test_name// /_}.result"
}

# Test baseline shell performance
test_baseline() {
    log "Testing baseline shell performance..."
    
    benchmark "echo command" "echo 'baseline test'" $ITERATIONS
    benchmark "ls command" "ls /tmp >/dev/null" $ITERATIONS
    benchmark "pwd command" "pwd >/dev/null" $ITERATIONS
    benchmark "env command" "env >/dev/null" $ITERATIONS
}

# Test preexec hook overhead
test_preexec_overhead() {
    log "Testing preexec hook overhead..."
    
    # Test the hook components individually
    benchmark "base64 encoding" "echo -n 'test command' | base64 >/dev/null" $ITERATIONS
    benchmark "environment capture" "env | base64 -w 0 >/dev/null" $ITERATIONS
    benchmark "pwd capture" "pwd >/dev/null" $ITERATIONS
    
    # Test the full hook execution (without network)
    benchmark "full hook (no network)" "
        $TOTALRECALL_ROOT/bin/preexec-hook \
            -command=\$(echo -n 'test command' | base64) \
            -pwd=\$(pwd) \
            -env=\$(env | head -20 | base64 -w 0) \
            -return-code=0 \
            -start-timestamp=\$(date --iso-8601=seconds) \
            -end-timestamp=\$(date --iso-8601=seconds) \
            -host=127.0.0.1 \
            -port=99999 \
            -timeout=1s \
            -env-config=\$HOME/.totalrecall/env-config.json 2>/dev/null || true
    " 50  # Fewer iterations for this expensive test
}

# Test environment filtering performance
test_filtering_performance() {
    log "Testing environment filtering performance..."
    
    # Create large environment
    for i in {1..200}; do
        export "BENCH_VAR_$i"="value_$i"
    done
    
    benchmark "large env test mode" "
        $TOTALRECALL_ROOT/bin/preexec-hook \
            -test \
            -env-config=\$HOME/.totalrecall/env-config.json >/dev/null
    " 20
    
    # Cleanup
    for i in {1..200}; do
        unset "BENCH_VAR_$i"
    done
}

# Test network overhead (if services are running)
test_network_overhead() {
    log "Testing network overhead..."
    
    if curl -s -f http://localhost:5170 >/dev/null 2>&1; then
        benchmark "full hook (with network)" "
            $TOTALRECALL_ROOT/bin/preexec-hook \
                -command=\$(echo -n 'network test' | base64) \
                -pwd=\$(pwd) \
                -env=\$(env | head -10 | base64 -w 0) \
                -return-code=0 \
                -start-timestamp=\$(date --iso-8601=seconds) \
                -end-timestamp=\$(date --iso-8601=seconds) \
                -host=127.0.0.1 \
                -port=5170 \
                -timeout=5s \
                -env-config=\$HOME/.totalrecall/env-config.json 2>/dev/null || true
        " 20
    else
        warning "Fluent Bit not running, skipping network overhead test"
    fi
}

# Test real preexec function overhead
test_preexec_function() {
    log "Testing preexec function overhead..."
    
    # Source the preexec functions
    source "$TOTALRECALL_ROOT/scripts/preexec.sh" 2>/dev/null || {
        warning "Could not source preexec.sh, skipping function test"
        return
    }
    
    benchmark "preexec function" "preexec 'echo test'" 50
    
    # Mock precmd to avoid network calls
    precmd_mock() {
        local ___RETURN_CODE=$?
        # Skip the network calls, just test the overhead
        unset ___PREEXEC_CMD
        unset ___PREEXEC_START_TIMESTAMP
        unset ___PREEXEC_PWD
        unset ___PREEXEC_ENV
    }
    
    benchmark "precmd function (mocked)" "precmd_mock" 50
}

# Memory usage test
test_memory_usage() {
    log "Testing memory usage..."
    
    # Get baseline memory
    baseline_memory=$(ps -o pid,vsz,rss -p $$ | tail -1 | awk '{print $3}')
    echo "Baseline memory: ${baseline_memory}KB"
    
    # Create large environment and test
    for i in {1..500}; do
        export "MEM_TEST_VAR_$i"="$(head -c 100 /dev/zero | tr '\0' 'a')"
    done
    
    # Run filtering test
    $TOTALRECALL_ROOT/bin/preexec-hook -test -env-config="$HOME/.totalrecall/env-config.json" >/dev/null
    
    # Check memory after
    after_memory=$(ps -o pid,vsz,rss -p $$ | tail -1 | awk '{print $3}')
    memory_diff=$((after_memory - baseline_memory))
    
    echo "Memory after large env: ${after_memory}KB"
    echo "Memory difference: ${memory_diff}KB"
    
    # Cleanup
    for i in {1..500}; do
        unset "MEM_TEST_VAR_$i"
    done
    
    if [[ $memory_diff -gt 10000 ]]; then  # More than 10MB
        warning "High memory usage detected: ${memory_diff}KB"
    else
        success "Memory usage acceptable: ${memory_diff}KB"
    fi
}

# Performance analysis
analyze_results() {
    log "Analyzing performance results..."
    echo ""
    
    # Read all benchmark results
    declare -A results
    for file in /tmp/benchmark_*.result; do
        if [[ -f "$file" ]]; then
            test_name=$(basename "$file" .result | sed 's/benchmark_//; s/_/ /g')
            result=$(cat "$file")
            results["$test_name"]="$result"
        fi
    done
    
    echo "📊 Performance Summary:"
    echo "======================"
    
    # Show results sorted by time
    for test in $(printf '%s\n' "${!results[@]}" | sort); do
        time="${results[$test]}"
        if (( $(echo "$time > 100" | bc -l) )); then
            echo -e "${YELLOW}⚠️  $test: ${time}ms${NC}"
        elif (( $(echo "$time > 50" | bc -l) )); then
            echo -e "${YELLOW}⚠️  $test: ${time}ms${NC}"
        else
            echo -e "${GREEN}✅ $test: ${time}ms${NC}"
        fi
    done
    
    echo ""
    
    # Performance recommendations
    echo "🎯 Performance Recommendations:"
    echo "==============================="
    
    if [[ -n "${results[full hook (no network)]}" ]]; then
        hook_time="${results[full hook (no network)]}"
        if (( $(echo "$hook_time > 50" | bc -l) )); then
            warning "Hook execution is slow (${hook_time}ms). Consider:"
            echo "  - Reducing environment variable allowlist"
            echo "  - Using shorter hash lengths"
            echo "  - Optimizing regex patterns"
        else
            success "Hook execution time acceptable (${hook_time}ms)"
        fi
    fi
    
    if [[ -n "${results[full hook (with network)]}" ]]; then
        network_time="${results[full hook (with network)]}"
        if (( $(echo "$network_time > 200" | bc -l) )); then
            warning "Network overhead is high (${network_time}ms). Consider:"
            echo "  - Using local buffering/batching"
            echo "  - Reducing timeout values"
            echo "  - Optimizing Fluent Bit configuration"
        else
            success "Network overhead acceptable (${network_time}ms)"
        fi
    fi
    
    echo ""
    echo "💡 Performance Guidelines:"
    echo "========================="
    echo "- Hook execution: < 50ms (excellent), < 100ms (acceptable)"
    echo "- Network calls: < 200ms (acceptable), < 500ms (tolerable)"
    echo "- Memory usage: < 10MB additional (acceptable)"
    echo "- Environment processing: < 20ms for 100 variables"
    echo ""
    
    # Cleanup
    rm -f /tmp/benchmark_*.result
}

# Main execution
main() {
    echo "🚀 Total Recall Performance Benchmark"
    echo "====================================="
    echo ""
    echo "This benchmark measures the performance overhead of Total Recall"
    echo "command capture on your shell environment."
    echo ""
    
    # Check prerequisites
    if [[ ! -f "$TOTALRECALL_ROOT/bin/preexec-hook" ]]; then
        echo "❌ preexec-hook binary not found. Build it first:"
        echo "   cd tools/preexec-hook && go build -o ../../bin/preexec-hook"
        exit 1
    fi
    
    if ! command -v bc >/dev/null; then
        echo "❌ 'bc' calculator not found. Install it first:"
        echo "   # Ubuntu/Debian: sudo apt install bc"
        echo "   # macOS: brew install bc"
        exit 1
    fi
    
    # Generate config if it doesn't exist
    if [[ ! -f "$HOME/.totalrecall/env-config.json" ]]; then
        log "Generating default config..."
        "$TOTALRECALL_ROOT/bin/preexec-hook" -generate-config
    fi
    
    # Run benchmarks
    test_baseline
    test_preexec_overhead
    test_filtering_performance
    test_network_overhead
    test_preexec_function
    test_memory_usage
    
    # Analyze results
    analyze_results
    
    echo "🎉 Performance benchmark complete!"
    echo ""
    echo "If performance is not acceptable, consider:"
    echo "1. Reducing environment variable allowlist"
    echo "2. Using asynchronous processing"
    echo "3. Implementing local caching/batching"
    echo "4. Optimizing regex patterns in config"
}

# Handle arguments
case "${1:-}" in
    --help|-h)
        echo "Usage: $0 [options]"
        echo ""
        echo "Options:"
        echo "  --iterations N    Number of test iterations (default: $ITERATIONS)"
        echo "  --baseline        Only run baseline tests"
        echo "  --hook            Only test hook overhead"
        echo "  --network         Only test network overhead"
        echo "  --memory          Only test memory usage"
        echo ""
        exit 0
        ;;
    --iterations)
        ITERATIONS="$2"
        shift 2
        main
        ;;
    --baseline)
        test_baseline
        ;;
    --hook)
        test_preexec_overhead
        ;;
    --network)
        test_network_overhead
        ;;
    --memory)
        test_memory_usage
        ;;
    "")
        main
        ;;
    *)
        echo "Unknown option: $1"
        echo "Run $0 --help for usage information."
        exit 1
        ;;
esac
