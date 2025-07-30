#!/bin/bash

# Test Health Monitoring Endpoints
# This script tests the comprehensive health monitoring system

echo "🏥 Testing Health Monitoring System"
echo "=================================="
echo

# Start API server in background
echo "Starting API server..."
./argus-sdr api &
API_PID=$!

# Wait for server to start
sleep 3

# Test health monitoring endpoints
echo "Testing health monitoring endpoints..."
echo

# 1. Test liveness probe
echo "1. Testing liveness endpoint:"
curl -s http://localhost:8080/liveness | jq '.' || echo "JSON parsing failed"
echo

# 2. Test readiness probe  
echo "2. Testing readiness endpoint:"
curl -s http://localhost:8080/readiness | jq '.' || echo "JSON parsing failed"
echo

# 3. Test comprehensive health check
echo "3. Testing comprehensive health endpoint:"
curl -s http://localhost:8080/health | jq '.' || echo "JSON parsing failed"
echo

# 4. Test metrics endpoint 
echo "4. Testing metrics endpoint:"
curl -s http://localhost:8080/metrics | jq '.' || echo "JSON parsing failed"
echo

# 5. Test version endpoint (existing)
echo "5. Testing version endpoint:"
curl -s http://localhost:8080/version | jq '.' || echo "JSON parsing failed"  
echo

# Test with some load to generate metrics
echo "6. Generating some load for metrics testing..."
for i in {1..10}; do
    curl -s http://localhost:8080/health > /dev/null &
    curl -s http://localhost:8080/liveness > /dev/null &  
    curl -s http://localhost:8080/readiness > /dev/null &
done
wait

sleep 1

echo "7. Testing metrics after load:"
curl -s http://localhost:8080/metrics | jq '.response_time_stats' || echo "JSON parsing failed"
echo

# Cleanup
echo "Cleaning up..."
kill $API_PID
wait $API_PID 2>/dev/null

echo "✅ Health monitoring tests completed!"
echo
echo "Health monitoring system provides:"
echo "- /liveness: Simple liveness check for Kubernetes" 
echo "- /readiness: Database connectivity check"
echo "- /health: Comprehensive system health with component status"
echo "- /metrics: Detailed performance metrics and statistics"
echo "- /version: Application version information"