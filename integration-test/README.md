# Integration Test for Drop-in Replacement

This directory contains an integration test that verifies the `newrelic-otel-shim` can be used as a drop-in replacement for the official New Relic Go Agent v3.

## Test Application

The test application (`main.go`) is a comprehensive example that uses various New Relic Go Agent v3 APIs:

- **Basic Transactions**: Creating and managing transactions
- **Web Transactions**: HTTP request/response handling  
- **Database Segments**: MySQL, PostgreSQL operations
- **External Segments**: HTTP client calls
- **Custom Segments**: Generic instrumentation
- **Message Segments**: Queue/topic messaging
- **Custom Events**: Recording custom telemetry
- **Error Handling**: Error reporting and tracking
- **Context Propagation**: Goroutine context handling

## Test Process

The integration test (`test-drop-in-replacement.sh`) performs the following steps:

### Phase 1: Original New Relic Agent
1. **Build**: Compiles the test application with the official New Relic Go Agent v3
2. **Run**: Executes the application to verify it works correctly
3. **Verify**: Confirms all New Relic APIs function as expected

### Phase 2: Shim Replacement  
1. **Replace**: Adds a `replace` directive in `go.mod` to use the shim instead
2. **Build**: Recompiles the application with the shim (no code changes needed)
3. **Run**: Executes the application with the shim
4. **Verify**: Confirms all functionality still works

### Phase 3: Verification
1. **Binary Comparison**: Verifies both versions build successfully
2. **API Compatibility**: Confirms no import changes were required
3. **Functionality**: Ensures both versions produce executable binaries

### Phase 4: Cleanup
1. **Restore**: Returns `go.mod` to original state
2. **Clean**: Removes test artifacts

## Running the Test

### Local Testing
```bash
# From repository root
make integration-test

# Or directly
cd integration-test
chmod +x test-drop-in-replacement.sh
./test-drop-in-replacement.sh
```

### CI/CD Integration
The test runs automatically in GitHub Actions as part of the CI pipeline, ensuring every commit and pull request validates the drop-in replacement functionality.

## Expected Output

When successful, the test will show:

```
🎉 SUCCESS: New Relic Go Agent Drop-in Replacement Test Completed!
==================================================================

✅ The newrelic-otel-shim successfully functions as a drop-in replacement
✅ Original application builds and runs correctly
✅ Application with shim builds and runs correctly  
✅ No source code changes required
✅ All New Relic APIs are compatible

🚀 The shim is ready for production use!
```

## What This Proves

This integration test demonstrates that:

1. **Zero Code Changes**: Applications can switch to the shim without modifying any source code
2. **API Compatibility**: All New Relic v3 APIs work identically with the shim
3. **Build Compatibility**: The shim has the same build requirements as the original agent
4. **Runtime Compatibility**: Applications run correctly with either the original agent or the shim
5. **Drop-in Replacement**: The shim truly is a drop-in replacement, requiring only a `go.mod` change

This gives users confidence that they can adopt the shim incrementally and safely in their existing applications.