#!/bin/bash

# SDR API Test Script
# This script demonstrates the basic functionality of the SDR API

API_BASE="http://localhost:8080"

echo "🚀 Starting SDR API Tests..."
echo

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Function to print colored output
print_step() {
    echo -e "${BLUE}📍 $1${NC}"
}

print_success() {
    echo -e "${GREEN}✅ $1${NC}"
}

print_error() {
    echo -e "${RED}❌ $1${NC}"
}

print_info() {
    echo -e "${YELLOW}ℹ️  $1${NC}"
}

# Test health endpoint
print_step "Testing health endpoint..."
HEALTH_RESPONSE=$(curl -s "$API_BASE/health")
if echo "$HEALTH_RESPONSE" | grep -q "ok"; then
    print_success "Health check passed"
else
    print_error "Health check failed"
    exit 1
fi
echo

# Register Type 1 client (SDR device)
print_step "Registering Type 1 client (SDR Device)..."
TYPE1_REGISTER_RESPONSE=$(curl -s -X POST "$API_BASE/api/auth/register" \
    -H "Content-Type: application/json" \
    -d '{
        "email": "sdr1@example.com",
        "password": "password123",
        "client_type": 1
    }')

if echo "$TYPE1_REGISTER_RESPONSE" | grep -q "token"; then
    TYPE1_TOKEN=$(echo "$TYPE1_REGISTER_RESPONSE" | grep -o '"token":"[^"]*"' | cut -d'"' -f4)
    print_success "Type 1 client registered successfully"
    print_info "Token: ${TYPE1_TOKEN:0:20}..."
else
    print_error "Type 1 client registration failed"
    echo "Response: $TYPE1_REGISTER_RESPONSE"
fi
echo

# Register Type 1 client device
if [ ! -z "$TYPE1_TOKEN" ]; then
    print_step "Registering Type 1 client device..."
    CLIENT_REGISTER_RESPONSE=$(curl -s -X POST "$API_BASE/api/type1/register" \
        -H "Authorization: Bearer $TYPE1_TOKEN" \
        -H "Content-Type: application/json" \
        -d '{
            "client_name": "SDR Device 1",
            "capabilities": "{\"frequency_range\": \"88-108MHz\", \"sample_rate\": \"2.4MHz\"}"
        }')

    if echo "$CLIENT_REGISTER_RESPONSE" | grep -q "SDR Device 1"; then
        print_success "Type 1 client device registered"
    else
        print_error "Type 1 client device registration failed"
        echo "Response: $CLIENT_REGISTER_RESPONSE"
    fi
fi
echo

# Register Type 2 client (Data consumer)
print_step "Registering Type 2 client (Data Consumer)..."
TYPE2_REGISTER_RESPONSE=$(curl -s -X POST "$API_BASE/api/auth/register" \
    -H "Content-Type: application/json" \
    -d '{
        "email": "consumer@example.com",
        "password": "password123",
        "client_type": 2
    }')

if echo "$TYPE2_REGISTER_RESPONSE" | grep -q "token"; then
    TYPE2_TOKEN=$(echo "$TYPE2_REGISTER_RESPONSE" | grep -o '"token":"[^"]*"' | cut -d'"' -f4)
    print_success "Type 2 client registered successfully"
    print_info "Token: ${TYPE2_TOKEN:0:20}..."
else
    print_error "Type 2 client registration failed"
    echo "Response: $TYPE2_REGISTER_RESPONSE"
fi
echo

# Test Type 2 client endpoints
if [ ! -z "$TYPE2_TOKEN" ]; then
    print_step "Checking Type 1 client availability..."
    AVAILABILITY_RESPONSE=$(curl -s -X GET "$API_BASE/api/data/availability" \
        -H "Authorization: Bearer $TYPE2_TOKEN")

    if echo "$AVAILABILITY_RESPONSE" | grep -q "connected_clients"; then
        print_success "Availability check successful"
        print_info "Response: $AVAILABILITY_RESPONSE"
    else
        print_error "Availability check failed"
        echo "Response: $AVAILABILITY_RESPONSE"
    fi
    echo

    print_step "Requesting spectrum data..."
    SPECTRUM_RESPONSE=$(curl -s -X GET "$API_BASE/api/data/spectrum" \
        -H "Authorization: Bearer $TYPE2_TOKEN")

    if echo "$SPECTRUM_RESPONSE" | grep -q "spectrum_data"; then
        print_success "Spectrum data request successful"
        print_info "Got mock spectrum data"
    else
        print_error "Spectrum data request failed"
        echo "Response: $SPECTRUM_RESPONSE"
    fi
    echo

    print_step "Requesting signal analysis..."
    SIGNAL_RESPONSE=$(curl -s -X GET "$API_BASE/api/data/signal" \
        -H "Authorization: Bearer $TYPE2_TOKEN")

    if echo "$SIGNAL_RESPONSE" | grep -q "signal_analysis"; then
        print_success "Signal analysis request successful"
        print_info "Got mock signal analysis data"
    else
        print_error "Signal analysis request failed"
        echo "Response: $SIGNAL_RESPONSE"
    fi
fi
echo

# Test authentication
print_step "Testing authentication with Type 1 token..."
ME_RESPONSE=$(curl -s -X GET "$API_BASE/api/auth/me" \
    -H "Authorization: Bearer $TYPE1_TOKEN")

if echo "$ME_RESPONSE" | grep -q "sdr1@example.com"; then
    print_success "Authentication working correctly"
else
    print_error "Authentication failed"
    echo "Response: $ME_RESPONSE"
fi
echo

# Test cross-client-type access (should fail)
print_step "Testing cross-client-type access protection..."
CROSS_ACCESS_RESPONSE=$(curl -s -X GET "$API_BASE/api/data/spectrum" \
    -H "Authorization: Bearer $TYPE1_TOKEN")

if echo "$CROSS_ACCESS_RESPONSE" | grep -q "Access denied"; then
    print_success "Cross-client-type protection working"
else
    print_error "Cross-client-type protection failed"
    echo "Response: $CROSS_ACCESS_RESPONSE"
fi
echo

print_step "Tests completed!"
echo
print_info "To test WebSocket connection, you can use a WebSocket client:"
print_info "ws://localhost:8080/ws (with Authorization header)"
echo
print_info "Example WebSocket test with wscat:"
print_info "wscat -c 'ws://localhost:8080/ws' -H 'Authorization: Bearer $TYPE1_TOKEN'"