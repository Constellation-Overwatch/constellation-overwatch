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
git clone https://github.com/Constellation-Overwatch/constellation-overwatch.git
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
export TOKEN="constellation-dev-token" # or what you changed in .env

# 1. Create an organization
curl -s -X POST http://localhost:8080/api/v1/organizations \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "My Fleet",
    "org_type": "civilian", # allowed enums 'military', 'civilian' 'commercial', 'ngo'
    "description": "Test drone fleet"
  }'
```

You will thin get a response like this:
```json
{"success":true,"data":{"org_id":"ae9c65d0-b5f3-4cec-8ffa-68ff1173e050","name":"My Fleet","org_type":"civilian","metadata":"{}","created_at":"2025-10-22T11:34:29.195678-05:00","updated_at":"2025-10-22T11:34:29.195678-05:00"}}
```

Start registering entities to this organization id from above at `data.org_id`:
```sh
export ORG_ID='ae9c65d0-b5f3-4cec-8ffa-68ff1173e050'
curl -s -X POST "http://localhost:8080/api/v1/entities?org_id=$ORG_ID" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Drone-001",
    "entity_type": "aircraft_multirotor",
    "description": "Primary vegetation inspection drone",
    "metadata": {
      "model": "DJI-M300",
      "serial": "ABC123456"
    }
  }'
```
You will then get a response like this on the client:
```json
{"success":true,"data":{"entity_id":"5458eec0-b0e3-4290-8db5-17936dbbfc64","org_id":"ae9c65d0-b5f3-4cec-8ffa-68ff1173e050","entity_type":"aircraft_multirotor","status":"unknown","priority":"normal","is_live":false,"metadata":"{\"model\":\"DJI-M300\",\"serial\":\"ABC123456\"}","created_at":"2025-10-22T11:41:11.069194-05:00","updated_at":"2025-10-22T11:41:11.069194-05:00"}}
```

You will see this log on your `constellation-overwatch` c2 server logs:
```sh
2025/10/22 11:41:11 [EntityWorker] Received entity message on subject: constellation.entities.ae9c65d0-b5f3-4cec-8ffa-68ff1173e050.created
2025/10/22 11:41:11 [EntityWorker] Entity data:
{
  "data": {
    "entity": {
      "created_at": "2025-10-22T11:41:11.069194-05:00",
      "entity_id": "5458eec0-b0e3-4290-8db5-17936dbbfc64",
      "entity_type": "aircraft_multirotor",
      "is_live": false,
      "metadata": "{\"model\":\"DJI-M300\",\"serial\":\"ABC123456\"}",
      "org_id": "ae9c65d0-b5f3-4cec-8ffa-68ff1173e050",
      "priority": "normal",
      "status": "unknown",
      "updated_at": "2025-10-22T11:41:11.069194-05:00"
    },
    "entity_id": "5458eec0-b0e3-4290-8db5-17936dbbfc64",
    "entity_type": "aircraft_multirotor",
    "org_id": "ae9c65d0-b5f3-4cec-8ffa-68ff1173e050",
    "priority": "normal",
    "status": "unknown"
  },
  "id": "15ad3df9-7762-49e2-8dd0-e2f2a42daecd",
  "source": "entity-service",
  "subject": "constellation.entities.ae9c65d0-b5f3-4cec-8ffa-68ff1173e050.created",
  "timestamp": "2025-10-22T16:41:11.071809Z",
  "type": "created"
}
```

Extract `data.entity_id`
```sh
export ENTITY_ID='5458eec0-b0e3-4290-8db5-17936dbbfc64'
```

Get all entities in the organization
```sh
curl -s "http://localhost:8080/api/v1/entities?org_id=$ORG_ID" \
  -H "Authorization: Bearer $TOKEN" | jq
```

Get specific entity details
```sh
curl -s "http://localhost:8080/api/v1/entities?org_id=$ORG_ID&entity_id=$ENTITY_ID" \
  -H "Authorization: Bearer $TOKEN" | jq
```

Update entity status
```sh
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

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## Roadmap

- [ ] WebSocket support for real-time updates
- [ ] Kubernetes deployment manifests
- [ ] Prometheus metrics integration
- [ ] Video stream proxy support
- [ ] Multi-region mesh networking
- [ ] Edge client SDK (Go, Python, Rust) - overwatch-sdk
- [ ] Web dashboard UI
- [ ] Mobile control application
