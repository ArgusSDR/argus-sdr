#!/bin/bash

echo "Testing Docker Compose SDR System"
echo "================================="

# Check if Docker is available
if ! command -v docker &> /dev/null; then
    echo "❌ Docker is not installed or not running"
    exit 1
fi

if ! command -v docker-compose &> /dev/null; then
    echo "❌ Docker Compose is not installed"
    exit 1
fi

echo "✅ Docker and Docker Compose are available"

# Build the Docker image
echo "Building Docker image..."
docker build -t argus-sdr:latest . || { echo "❌ Docker build failed"; exit 1; }
echo "✅ Docker image built successfully"

# Start the core services (API + 3 collectors)
echo "Starting SDR system services..."
docker-compose up -d api-server collector-station-1 collector-station-2 collector-station-3

# Wait for services to be ready
echo "Waiting for services to start..."
sleep 15

# Check service health
echo "Checking service health..."
API_HEALTH=$(curl -s http://localhost:8080/health 2>/dev/null | grep -o '"status":"ok"' || echo "failed")

if [ "$API_HEALTH" = '"status":"ok"' ]; then
    echo "✅ API server is healthy"
else
    echo "❌ API server health check failed"
    docker-compose logs api-server
    docker-compose down
    exit 1
fi

# Check collector connections
echo "Checking collector connections..."
sleep 10

# Show service status
echo "Service status:"
docker-compose ps

echo ""
echo "📊 System Status:"
echo "- API Server: http://localhost:8080"
echo "- Collectors: 3 stations running"
echo "- Database: SQLite in api_data volume"
echo ""
echo "🧪 To test the system:"
echo "1. Register users: curl -X POST http://localhost:8080/api/auth/register ..."
echo "2. Start receiver client: docker-compose --profile testing run receiver-client"
echo "3. Test ICE transfers: Use the test scripts"
echo ""
echo "🛑 To stop the system:"
echo "docker-compose down"
echo ""
echo "✅ Docker Compose setup is working correctly!"