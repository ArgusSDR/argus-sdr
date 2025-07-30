#!/bin/bash

echo "Testing ICE Direct Transfer Integration"
echo "===================================="

# Build the application
echo "Building application..."
go build -o argus-sdr . || { echo "❌ Build failed"; exit 1; }
echo "✅ Build successful"

# Start API server in background
echo "Starting API server..."
SERVER_ADDRESS=:8081 ./argus-sdr api &
API_PID=$!

# Wait for server to start
sleep 2

# Test the health endpoint
echo "Testing health endpoint..."
curl -s http://localhost:8081/health | grep '"status":"ok"' > /dev/null || { 
    echo "❌ Health check failed"
    kill $API_PID
    exit 1
}
echo "✅ Health check successful"

# Register a test user (Type 2 - receiver)
echo "Registering test receiver user..."
REGISTER_RESPONSE=$(curl -s -X POST http://localhost:8081/api/auth/register \
  -H "Content-Type: application/json" \
  -d '{
    "email": "test-receiver@example.com",
    "password": "testpass123",
    "client_type": 2
  }')

echo "Register response: $REGISTER_RESPONSE"

# Login to get token
echo "Logging in..."
LOGIN_RESPONSE=$(curl -s -X POST http://localhost:8081/api/auth/login \
  -H "Content-Type: application/json" \
  -d '{
    "email": "test-receiver@example.com",
    "password": "testpass123"
  }')

TOKEN=$(echo $LOGIN_RESPONSE | grep -o '"token":"[^"]*"' | cut -d'"' -f4)

if [ -z "$TOKEN" ]; then
    echo "❌ Login failed - no token received"
    echo "Login response: $LOGIN_RESPONSE"
    kill $API_PID
    exit 1
fi

echo "✅ Login successful, token: ${TOKEN:0:20}..."

# Test the new ICE-enabled data request endpoint
echo "Testing ICE-enabled data request..."
ICE_REQUEST_RESPONSE=$(curl -s -X POST http://localhost:8081/api/data/request-ice \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{
    "request_type": "samples",
    "parameters": "{\"frequency\": 100.0, \"sample_rate\": 1000000, \"duration\": 10}",
    "use_ice": true,
    "station_id": "test-station-001"
  }')

echo "ICE request response: $ICE_REQUEST_RESPONSE"

# Check if we got a session_id
SESSION_ID=$(echo $ICE_REQUEST_RESPONSE | grep -o '"session_id":"[^"]*"' | cut -d'"' -f4)

if [ -z "$SESSION_ID" ]; then
    echo "❌ ICE request failed - no session_id received"
    kill $API_PID
    exit 1
fi

echo "✅ ICE session created: $SESSION_ID"

# Test ICE session endpoints
echo "Testing ICE session retrieval..."
ICE_SESSIONS_RESPONSE=$(curl -s -X GET http://localhost:8081/api/ice/sessions \
  -H "Authorization: Bearer $TOKEN")

echo "ICE sessions response: $ICE_SESSIONS_RESPONSE"

# Check if our session appears in the list
if echo $ICE_SESSIONS_RESPONSE | grep -q "$SESSION_ID"; then
    echo "✅ ICE session found in active sessions"
else
    echo "❌ ICE session not found in active sessions"
    kill $API_PID
    exit 1
fi

# Clean up
echo "Cleaning up..."
kill $API_PID
wait $API_PID 2>/dev/null

echo ""
echo "🎉 ICE Integration Test Summary:"
echo "- ✅ API server startup"
echo "- ✅ User registration and authentication"
echo "- ✅ ICE-enabled data request endpoint"
echo "- ✅ ICE session creation"
echo "- ✅ ICE session retrieval"
echo ""
echo "ICE direct transfer integration is working correctly!"