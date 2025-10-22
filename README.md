# Constellation Overwatch

Open source Edge C2 (Command & Control) Server Mesh Fabric designed for hybrid drone/robotic pub/sub communication, sensor data streaming, and video stream management.

## Overview

Constellation Overwatch provides a distributed, event-driven architecture for managing fleets of autonomous systems including drones, robots, IoT sensors, and edge computing devices. Built on NATS JetStream for reliable, low-latency messaging with atomic operations and durable streams.

## Key Features

- **Real-time Pub/Sub Messaging** - Low-latency communication between edge devices and control systems
- **Durable Event Streams** - Reliable message delivery with JetStream persistence
- **Multi-Entity Support** - Manage drones, robots, sensors, and other autonomous systems
- **RESTful API** - Simple HTTP interface with bearer token authentication
- **Embedded NATS** - Self-contained messaging system with no external dependencies
- **Telemetry Streaming** - Efficient handling of high-frequency sensor data
- **Command & Control** - Secure command distribution to edge devices

## Architecture

### API Service Diagram

```mermaid
graph TB
    subgraph "Client Layer"
        C1[Web Dashboard]
        C2[Mobile App]
        C3[CLI Tools]
        C4[Edge Devices]
    end
    
    subgraph "API Gateway"
        API[REST API<br/>:8080]
        AUTH[Bearer Auth<br/>Middleware]
    end
    
    subgraph "Core Services"
        OS[Organization<br/>Service]
        ES[Entity<br/>Service]
    end
    
    subgraph "Data Layer"
        DB[(SQLite DB)]
        NATS[NATS JetStream<br/>:4222]
    end
    
    subgraph "NATS Streams"
        S1[CONSTELLATION_ENTITIES]
        S2[CONSTELLATION_EVENTS]
        S3[CONSTELLATION_TELEMETRY]
        S4[CONSTELLATION_COMMANDS]
    end
    
    C1 & C2 & C3 --> API
    C4 <--> NATS
    API --> AUTH
    AUTH --> OS & ES
    OS & ES --> DB
    ES --> NATS
    NATS --> S1 & S2 & S3 & S4
    
    style API fill:#4CAF50
    style NATS fill:#2196F3
    style DB fill:#FF9800
```

### Data Flow Sequence Diagram

```mermaid
sequenceDiagram
    participant D as Drone/Robot
    participant N as NATS JetStream
    participant A as API Service
    participant DB as Database
    participant C as Control Client
    
    Note over D,C: Entity Registration Flow
    C->>A: POST /api/v1/entities
    A->>DB: Store Entity
    A->>N: Publish entity.created
    A-->>C: Return Entity
    
    Note over D,C: Telemetry Flow
    D->>N: Publish telemetry data
    N->>N: Store in TELEMETRY stream
    C->>A: Subscribe to telemetry
    N-->>C: Stream telemetry
    
    Note over D,C: Command Flow
    C->>A: Send command
    A->>N: Publish to COMMANDS stream
    N->>D: Deliver command
    D->>N: Publish command.ack
    N-->>A: Command acknowledged
    A-->>C: Command status
    
    Note over D,C: Event Processing
    D->>N: Publish status change
    N->>N: Store in EVENTS stream
    A->>DB: Update entity status
    N-->>C: Notify subscribers
```

## Quick Start

### Prerequisites

- Go 1.21 or higher
- SQLite3

### Installation

```bash
# Clone the repository
git clone https://github.com/yourusername/constellation-overwatch.git
cd constellation-overwatch

# Install dependencies
go mod download

# Run the server
go run ./cmd/microlith/main.go
```

The server will start on port 8080 with an embedded NATS server on port 4222.

### Configuration

Create a `.env` file in the project root (copy from `.env.example`):

```bash
cp .env.example .env
```

Configuration options:

- `API_BEARER_TOKEN` - Bearer token for API authentication (default: `constellation-dev-token`)
- `PORT` - HTTP server port (default: `8080`)
- `DB_PATH` - SQLite database path (default: `./db/constellation.db`)
- `NATS_PORT` - NATS server port (default: `4222`)
- `NATS_DATA_DIR` - NATS data directory (default: `./data/nats`)

Example `.env` file:

```bash
API_BEARER_TOKEN=constellation-dev-token # default
PORT=8080
DB_PATH=./db/constellation.db
NATS_PORT=4222
NATS_DATA_DIR=./data/nats
```

### API Authentication

All API endpoints require Bearer token authentication:

```bash
curl -H "Authorization: Bearer constellation-dev-token" \
     http://localhost:8080/api/v1/organizations
```

### Quick Start with curl

Once the server is running, you can quickly provision an organization and create entities:

```bash
# Set your API token
export TOKEN="constellation-dev-token"

# 1. Create an organization
ORG_RESPONSE=$(curl -s -X POST http://localhost:8080/api/v1/organizations \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "My Fleet",
    "description": "Test drone fleet"
  }')

echo "Organization created: $ORG_RESPONSE"

# Extract org_id (requires jq)
ORG_ID=$(echo $ORG_RESPONSE | jq -r '.data.org_id')
echo "Organization ID: $ORG_ID"

# 2. Create a drone entity
DRONE_RESPONSE=$(curl -s -X POST "http://localhost:8080/api/v1/entities?org_id=$ORG_ID" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Drone-001",
    "entity_type": "drone",
    "description": "Primary surveillance drone",
    "metadata": {
      "model": "DJI-M300",
      "serial": "ABC123456"
    }
  }')

echo "Drone entity created: $DRONE_RESPONSE"

# Extract entity_id
ENTITY_ID=$(echo $DRONE_RESPONSE | jq -r '.data.entity_id')
echo "Entity ID: $ENTITY_ID"

# 3. Get all entities in the organization
curl -s "http://localhost:8080/api/v1/entities?org_id=$ORG_ID" \
  -H "Authorization: Bearer $TOKEN" | jq

# 4. Get specific entity details
curl -s "http://localhost:8080/api/v1/entities?org_id=$ORG_ID&entity_id=$ENTITY_ID" \
  -H "Authorization: Bearer $TOKEN" | jq

# 5. Update entity status
curl -s -X PUT "http://localhost:8080/api/v1/entities?org_id=$ORG_ID&entity_id=$ENTITY_ID" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "status": "active",
    "metadata": {
      "location": "lat:37.7749,lon:-122.4194",
      "battery": "85%"
    }
  }' | jq
```

**Without jq** (manual ID extraction):

```bash
export TOKEN="constellation-dev-token"

# Create organization
curl -X POST http://localhost:8080/api/v1/organizations \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"My Fleet","description":"Test fleet"}'

# Copy the org_id from response and use it
export ORG_ID="paste-org-id-here"

# Create entity
curl -X POST "http://localhost:8080/api/v1/entities?org_id=$ORG_ID" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"Drone-001","entity_type":"drone"}'

# List entities
curl "http://localhost:8080/api/v1/entities?org_id=$ORG_ID" \
  -H "Authorization: Bearer $TOKEN"
```

## API Endpoints

### Organizations
- `POST /api/v1/organizations` - Create organization
- `GET /api/v1/organizations` - List organizations
- `GET /api/v1/organizations?org_id=xxx` - Get organization

### Entities
- `POST /api/v1/entities?org_id=xxx` - Create entity
- `GET /api/v1/entities?org_id=xxx` - List entities
- `GET /api/v1/entities?org_id=xxx&entity_id=yyy` - Get entity
- `PUT /api/v1/entities?org_id=xxx&entity_id=yyy` - Update entity
- `DELETE /api/v1/entities?org_id=xxx&entity_id=yyy` - Delete entity

### Health Check
- `GET /health` - Service health status

## NATS Subjects

### Entity Events
- `constellation.entities.{org_id}.created`
- `constellation.entities.{org_id}.updated`
- `constellation.entities.{org_id}.deleted`
- `constellation.entities.{org_id}.status`

### Telemetry
- `constellation.telemetry.{org_id}.{entity_id}`

### Commands
- `constellation.commands.{org_id}.{entity_id}`
- `constellation.commands.{org_id}.broadcast`

## Development

### Project Structure

```
constellation-overwatch/
├── cmd/
│   └── microlith/         # Main application entry point
├── api/
│   ├── middleware/        # HTTP middleware (auth, CORS, logging)
│   ├── services/          # Business logic services
│   └── handlers.go        # HTTP request handlers
├── db/
│   ├── service.go         # Database service with auto-initialization
│   ├── schema.sql         # SQLite database schema
│   └── constellation.db   # SQLite database (auto-created)
├── pkg/
│   ├── ontology/          # Core domain models
│   ├── shared/            # Shared types and constants
│   └── services/
│       ├── embedded-nats/ # Embedded NATS server
│       └── workers/       # Background event processors
├── data/                  # NATS JetStream data directory
└── nats.conf              # NATS configuration
```

### Building

```bash
# Build the binary
go build -o constellation-overwatch ./cmd/microlith/main.go

# Run the binary
./constellation-overwatch

# Run tests
go test ./...
```

## License

MIT License - see LICENSE file for details

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## Roadmap

- [ ] WebSocket support for real-time updates
- [ ] Kubernetes deployment manifests
- [ ] Prometheus metrics integration
- [ ] Video stream proxy support
- [ ] Multi-region mesh networking
- [ ] Edge device SDK (Go, Python, Rust)
- [ ] Web dashboard UI
- [ ] Mobile control application
