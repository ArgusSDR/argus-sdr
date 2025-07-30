#!/bin/bash

# Test Advanced Collector Selection System
# This script demonstrates the new collector selection algorithms

echo "🎯 Testing Advanced Collector Selection System"
echo "=============================================="
echo

# Start API server in background
echo "Starting API server..."
./argus-sdr api &
API_PID=$!

# Wait for server to start
sleep 3

echo "Testing collector selection endpoints..."
echo

# Register a test user first
echo "1. Registering test user..."
curl -s -X POST http://localhost:8080/api/auth/register \
  -H "Content-Type: application/json" \
  -d '{
    "email": "test@example.com",
    "password": "test123",
    "client_type": 2
  }' > /dev/null

# Login to get token
echo "2. Getting authentication token..."
TOKEN=$(curl -s -X POST http://localhost:8080/api/auth/login \
  -H "Content-Type: application/json" \
  -d '{
    "email": "test@example.com",
    "password": "test123"
  }' | grep -o '"token":"[^"]*"' | cut -d'"' -f4)

if [ -z "$TOKEN" ]; then
    echo "❌ Failed to get authentication token"
    kill $API_PID
    exit 1
fi

echo "✅ Authentication successful"
echo

# Test collector metrics endpoint
echo "3. Testing collector metrics endpoint:"
curl -s -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/data/collector-metrics | jq '.' || echo "JSON parsing failed"
echo

# Test data request (should trigger advanced selection)
echo "4. Testing data request with advanced selection:"
curl -s -X POST http://localhost:8080/api/data/request \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "request_type": "samples",
    "parameters": "{\"frequency\": 100.0, \"sample_rate\": 1000000, \"duration\": 10, \"low_latency\": true}"
  }' | jq '.' || echo "JSON parsing failed"
echo

# Test another request with high capacity preference
echo "5. Testing data request with high capacity preference:"
curl -s -X POST http://localhost:8080/api/data/request \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "request_type": "samples", 
    "parameters": "{\"frequency\": 200.0, \"sample_rate\": 2000000, \"duration\": 30, \"high_capacity\": true}"
  }' | jq '.' || echo "JSON parsing failed"
echo

# Test metrics again to see if they updated
echo "6. Checking updated collector metrics:"
curl -s -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/data/collector-metrics | jq '.collectors' || echo "JSON parsing failed"
echo

# Cleanup
echo "Cleaning up..."
kill $API_PID
wait $API_PID 2>/dev/null

echo "✅ Advanced collector selection tests completed!"
echo
echo "Key features implemented:"
echo "- Load-balanced collector selection algorithm"
echo "- Performance metrics tracking (response time, success rate, load)"
echo "- Request requirements analysis (low latency, high capacity)"
echo "- Fallback to simple selection if advanced selection fails"
echo "- Real-time collector metrics endpoint for monitoring"
echo "- Support for multiple selection strategies:"
echo "  • Round Robin"
echo "  • Least Loaded"
echo "  • Best Performance"  
echo "  • Geographic"
echo "  • Weighted Random"
echo "  • Load Balanced (default)"