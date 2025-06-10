# Total Recall Testing Guide

This guide provides a comprehensive testing strategy to validate that your Total Recall installation works correctly and performs well.

## Quick Start Testing

### 1. Prerequisites Check
```bash
# Ensure binary is built
cd tools/preexec-hook && go build -o ../../bin/preexec-hook

# Verify Docker services are running
docker-compose up -d
docker-compose ps  # All services should be "Up"

# Check service health
curl -f http://localhost:9200/_cluster/health  # Elasticsearch
curl -f http://localhost:8443/api/status       # Kibana
```

### 2. Run Automated Test Suite
```bash
# Run all tests
chmod +x tests/test-suite.sh
./tests/test-suite.sh

# Or run specific test categories
./tests/test-suite.sh env-config      # Test environment filtering
./tests/test-suite.sh e2e             # Test end-to-end pipeline
./tests/test-suite.sh elasticsearch   # Test ES integration
```

### 3. Performance Validation
```bash
# Run performance benchmarks
chmod +x tests/performance-benchmark.sh
./tests/performance-benchmark.sh

# Check for acceptable performance:
# - Hook execution: < 50ms
# - Network calls: < 200ms  
# - Memory usage: < 10MB additional
```

## Detailed Testing Phases

### Phase 1: Component Testing

#### Environment Configuration System
```bash
# Test config generation and validation
./bin/preexec-hook -generate-config -env-config=/tmp/test-config.json
jq . /tmp/test-config.json  # Should show valid JSON structure

# Test environment filtering
export TEST_API_KEY="secret123"
export TEST_ENV="production"  
./bin/preexec-hook -test -env-config=/tmp/test-config.json

# Expected output:
# TEST_API_KEY=h8_a1b2c3d4 (hashed due to "key" pattern)
# TEST_ENV=production (plaintext, in allowlist)
```

#### Command Capture & Race Condition Fix
```bash
# Test that PWD is captured before command execution
export TOTAL_RECALL_ROOT=/path/to/totalrecall
source scripts/preexec.sh

# Test PWD race condition fix
pwd  # Note current directory
cd /tmp && ls  # Changes directory during execution

# Check Elasticsearch for the command:
curl -X POST 'http://localhost:9200/totalrecall*/_search' \
  -H 'Content-Type: application/json' \
  -d '{"query":{"match":{"command":"cd /tmp"}},"_source":["command","pwd"]}'

# The PWD should show your ORIGINAL directory, not /tmp
```

### Phase 2: Integration Testing

#### Elasticsearch Pipeline
```bash
# 1. Verify index template is applied
curl 'http://localhost:9200/_index_template/totalrecall'

# 2. Test document indexing
echo 'test command for elasticsearch'

# 3. Verify data appears (wait 2-3 seconds)
curl 'http://localhost:9200/totalrecall*/_search?pretty' | head -50

# 4. Check field mappings
curl 'http://localhost:9200/totalrecall*/_mapping' | jq '.[]'
```

#### Fluent Bit Integration
```bash
# Check Fluent Bit is receiving data
docker-compose logs fluent-bit | tail -20

# Test TLS connection
echo 'tls test command'
# Should see successful TLS connection in logs
```

### Phase 3: Real-World Usage Testing

#### Manual Testing Scenarios
```bash
# Run comprehensive manual tests
chmod +x tests/manual-test-scenarios.sh
./tests/manual-test-scenarios.sh
# Follow the interactive prompts
```

#### Key scenarios to verify:
1. **Environment context changes** - Commands grouped by environment
2. **Sensitive data protection** - API keys/passwords are hashed
3. **Performance impact** - No noticeable shell lag
4. **Error handling** - Graceful degradation when services are down

### Phase 4: Stress & Edge Case Testing

#### High Volume Testing
```bash
# Generate many commands quickly
for i in {1..50}; do
  echo "Stress test command $i"
  sleep 0.1
done

# Check all commands appear in Elasticsearch
curl -X POST 'http://localhost:9200/totalrecall*/_search' \
  -d '{"query":{"match":{"command":"Stress test"}},"size":0}' | \
  jq '.hits.total.value'  # Should be 50
```

#### Large Environment Testing
```bash
# Create large environment (500+ variables)
for i in {1..500}; do
  export "LARGE_VAR_$i"="value_$i"
done

echo 'large environment test'

# Verify reasonable performance
time ./bin/preexec-hook -test -env-config="$HOME/.totalrecall/env-config.json"
# Should complete in < 100ms
```

#### Edge Cases
```bash
# Test special characters in commands
echo 'command with "quotes" && pipes | grep test'

# Test very long commands
echo "$(head -c 1000 /dev/zero | tr '\0' 'a')"

# Test empty environment
env -i ./bin/preexec-hook -test -env-config="$HOME/.totalrecall/env-config.json"
```

## Kibana Validation

### Basic Functionality
1. Open http://localhost:8443
2. Navigate to Discover
3. Select "totalrecall*" index pattern
4. Verify recent commands appear

### Search Testing
```bash
# Test various search patterns in Kibana Discover:

# Failed commands
return_code:NOT 0

# Environment-specific commands  
env.NODE_ENV.keyword:"production"

# Commands in specific directory
pwd.keyword:"/path/to/your/project"

# Git commands
command:"git*"

# Hashed sensitive values
env.*:"h8_*"

# Time-based queries
start_timestamp:[now-1h TO now]
```

### Visualization Testing
1. Create a pie chart of commands by return_code
2. Create a timeline showing command frequency
3. Create a data table of most common commands
4. Verify all visualizations render correctly

## Configuration Drift Testing

### Drift Detection
```bash
# Test drift monitoring
./scripts/drift-monitor.sh

# Modify configuration
jq '.version = "1.0.1"' ~/.totalrecall/env-config.json > /tmp/new-config.json
mv /tmp/new-config.json ~/.totalrecall/env-config.json

# Verify update detection
./scripts/update-elasticsearch-template.sh --check-only
# Should exit with code 1 (update needed)

# Apply update
./scripts/update-elasticsearch-template.sh --backup
```

### Template Synchronization
```bash
# Test automatic template updates
echo 'command before config change'

# Add new variable to allowlist
jq '.allowlist.exact += ["NEW_TEST_VAR"]' ~/.totalrecall/env-config.json > /tmp/updated-config.json
mv /tmp/updated-config.json ~/.totalrecall/env-config.json

export NEW_TEST_VAR="new_value"
echo 'command after config change'

# Verify new variable is captured
curl -X POST 'http://localhost:9200/totalrecall*/_search' \
  -d '{"query":{"exists":{"field":"env.NEW_TEST_VAR"}}}'
```

## Troubleshooting Tests

### Common Issues
```bash
# 1. Commands not appearing in Elasticsearch
curl -f http://localhost:9200/_cluster/health  # ES healthy?
docker-compose logs fluent-bit                 # Fluent Bit errors?
./bin/preexec-hook -test                       # Hook working?

# 2. Environment variables not filtered correctly
./scripts/setup-env-config.sh validate         # Config valid?
./scripts/setup-env-config.sh test            # Filter test

# 3. Performance issues
./tests/performance-benchmark.sh               # Measure overhead
docker stats                                  # Resource usage

# 4. TLS/Certificate issues
openssl x509 -in certs/client.crt -text -noout  # Cert valid?
curl -k https://localhost:8443                   # TLS working?
```

### Debug Mode
```bash
# Enable verbose logging
export TOTAL_RECALL_DEBUG=1
echo 'debug test command'
# Check logs for detailed information
```

## Performance Benchmarks

### Acceptable Performance Thresholds

| Component | Excellent | Acceptable | Poor |
|-----------|-----------|------------|------|
| Hook execution | < 20ms | < 50ms | > 100ms |
| Network upload | < 100ms | < 200ms | > 500ms |
| Environment filtering | < 10ms | < 20ms | > 50ms |
| Memory overhead | < 5MB | < 10MB | > 20MB |

### Optimization Tips
If performance is poor:

1. **Reduce environment allowlist size**
   ```bash
   # Remove unnecessary patterns from allowlist
   ./scripts/setup-env-config.sh edit
   ```

2. **Optimize regex patterns**
   ```bash
   # Use specific matches instead of broad patterns
   # Good: "AWS_PROFILE"
   # Avoid: ".*AWS.*"
   ```

3. **Reduce hash length**
   ```go
   // In env_config.go, change:
   return fmt.Sprintf("h4_%x", hash[:2]) // 4 chars instead of 8
   ```

4. **Enable batching** (if implementing)
   ```bash
   # Buffer commands locally and send in batches
   export TOTAL_RECALL_BATCH_SIZE=10
   ```

## Validation Checklist

### ✅ Core Functionality
- [ ] Commands appear in Elasticsearch within 5 seconds
- [ ] PWD is captured correctly (race condition fixed)
- [ ] Environment variables are filtered according to config
- [ ] Sensitive variables are hashed, not stored in plaintext
- [ ] Return codes are captured accurately
- [ ] Timestamps are recorded correctly

### ✅ Security & Privacy
- [ ] API keys/passwords/secrets are hashed
- [ ] Configuration prevents sensitive data leakage
- [ ] mTLS connections work properly
- [ ] No plaintext credentials in Elasticsearch

### ✅ Performance
- [ ] No noticeable shell lag
- [ ] Hook execution < 50ms
- [ ] Memory usage reasonable (< 10MB overhead)
- [ ] Network calls don't block shell

### ✅ Operational
- [ ] Services start cleanly with docker-compose
- [ ] Graceful degradation when services are down
- [ ] Configuration changes are detected and applied
- [ ] Drift monitoring works correctly
- [ ] Backup and recovery procedures work

### ✅ Integration
- [ ] Kibana index pattern is created automatically
- [ ] Elasticsearch mappings are optimized
- [ ] Searches and visualizations work in Kibana
- [ ] Template updates work automatically

## Continuous Testing

### Daily Monitoring
```bash
# Add to cron for daily health checks
0 9 * * * /path/to/totalrecall/scripts/drift-monitor.sh

# Weekly performance check
0 9 * * 1 /path/to/totalrecall/tests/performance-benchmark.sh --baseline
```

### CI/CD Integration
```yaml
# Example GitHub Actions workflow
- name: Test Total Recall
  run: |
    docker-compose up -d
    sleep 30
    ./tests/test-suite.sh
    ./tests/performance-benchmark.sh
```

---

## Success Criteria

Your Total Recall installation is working correctly if:

1. **All automated tests pass** (test-suite.sh returns 0)
2. **Performance is acceptable** (< 50ms hook execution)
3. **Manual scenarios work** (commands visible in Kibana)
4. **Security is maintained** (sensitive data hashed)
5. **System is stable** (no errors in logs)

If any tests fail, check the troubleshooting section and verify your configuration matches the expected setup.
