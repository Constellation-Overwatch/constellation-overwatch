# Stateless API Service Architecture
## Multi-Organization Common Operating Picture Platform

### Architecture Overview

This document outlines a stateless, microservices-based architecture for the Constellation Overwatch platform, designed to support multi-organization common operating picture (COP) requirements with high scalability, security, and interoperability.

### Core Design Principles

1. **Stateless Services**: No server-side session state; all request context included in API calls
2. **Event-Driven**: State changes propagated through event streams
3. **Multi-Tenancy**: Logical isolation of organizational data with configurable sharing
4. **API-First**: All functionality exposed through well-defined REST/GraphQL/gRPC APIs
5. **Cloud-Native**: Containerized, orchestrated, and horizontally scalable

### Service Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                        API Gateway Layer                         │
├─────────────────────────────────────────────────────────────────┤
│  Load Balancer │ Rate Limiting │ Authentication │ API Routing   │
└────────────────┬───────────────────────────────────┬────────────┘
                 │                                   │
     ┌───────────▼───────────┐           ┌──────────▼──────────┐
     │   Core Services       │           │  Integration Layer   │
     ├───────────────────────┤           ├─────────────────────┤
     │ • Entity Service      │           │ • TAK Gateway       │
     │ • Command Service     │           │ • MAVLink Bridge    │
     │ • Mission Service     │           │ • ROS 2 Adapter     │
     │ • Telemetry Service   │           │ • Cloud Connectors  │
     │ • Analytics Service   │           └─────────────────────┘
     └───────────┬───────────┘
                 │
     ┌───────────▼───────────────────────────────────┐
     │           Event Streaming Layer               │
     ├───────────────────────────────────────────────┤
     │     Apache Kafka / AWS Kinesis / Redis       │
     └───────────┬───────────────────────────────────┘
                 │
     ┌───────────▼───────────────────────────────────┐
     │           Data Persistence Layer              │
     ├───────────────────────────────────────────────┤
     │ • PostgreSQL (Entities, Missions)             │
     │ • TimescaleDB (Telemetry, Tracks)            │
     │ • Redis (Cache, Real-time State)             │
     │ • S3/Blob (Media, Large Files)               │
     └───────────────────────────────────────────────┘
```

### Core Services

#### 1. Entity Service
Manages all trackable objects in the system.

**Endpoints:**
```yaml
POST   /api/v1/{org_id}/entities
GET    /api/v1/{org_id}/entities
GET    /api/v1/{org_id}/entities/{entity_id}
PUT    /api/v1/{org_id}/entities/{entity_id}
DELETE /api/v1/{org_id}/entities/{entity_id}
POST   /api/v1/{org_id}/entities/query
POST   /api/v1/{org_id}/entities/spatial-query
```

**Stateless Design:**
- No session state; authentication token contains org context
- Entity state retrieved from database on each request
- Updates published to event stream for real-time subscribers
- Spatial queries use PostGIS for efficient geospatial operations

**Example Request:**
```json
POST /api/v1/org-123/entities
Authorization: Bearer <token>

{
  "entity_type": "aircraft_multirotor",
  "position": {
    "latitude": 40.7128,
    "longitude": -74.0060,
    "altitude": 100
  },
  "metadata": {
    "callsign": "HAWK-1",
    "mission_id": "patrol-001"
  }
}
```

#### 2. Command Service
Handles command and control operations for vehicles and systems.

**Endpoints:**
```yaml
POST   /api/v1/{org_id}/commands
GET    /api/v1/{org_id}/commands/{command_id}/status
POST   /api/v1/{org_id}/commands/batch
DELETE /api/v1/{org_id}/commands/{command_id}
```

**Stateless Pattern:**
```json
POST /api/v1/org-123/commands
{
  "target_entity_id": "drone-001",
  "command_type": "navigate_to",
  "parameters": {
    "waypoint": {
      "latitude": 40.7589,
      "longitude": -73.9851,
      "altitude": 150
    },
    "speed": 10
  },
  "validation_token": "hash-of-current-state"
}
```

#### 3. Mission Service
Orchestrates complex multi-entity operations.

**Endpoints:**
```yaml
POST   /api/v1/{org_id}/missions
GET    /api/v1/{org_id}/missions
GET    /api/v1/{org_id}/missions/{mission_id}
PUT    /api/v1/{org_id}/missions/{mission_id}/status
POST   /api/v1/{org_id}/missions/{mission_id}/assign
```

**State Machine:**
```
DRAFT → PLANNED → ASSIGNED → ACTIVE → COMPLETED
                     ↓          ↓
                  CANCELLED   FAILED
```

#### 4. Telemetry Service
High-throughput ingestion and query of sensor data.

**Endpoints:**
```yaml
POST   /api/v1/{org_id}/telemetry/ingest
GET    /api/v1/{org_id}/telemetry/query
WS     /api/v1/{org_id}/telemetry/stream
```

**Time-Series Optimization:**
- Write-optimized for high ingestion rates
- Automatic data rollups and compression
- Configurable retention policies per org

### Multi-Organization Support

#### Organization Context
Every API request includes organization context through:
1. URL path parameter: `/api/v1/{org_id}/...`
2. JWT claims: `{"org_id": "...", "permissions": [...]}`
3. API key metadata: `X-Org-ID` header

#### Data Isolation Strategies

**1. Logical Isolation (Default)**
```sql
-- All queries automatically filtered by org_id
SELECT * FROM entities 
WHERE org_id = :current_org_id 
  AND entity_id = :requested_id;
```

**2. Physical Isolation (High Security)**
```yaml
# Separate databases per organization
org_military:
  database: constellation_mil
  encryption: AES-256
  
org_civilian:
  database: constellation_civ
  encryption: AES-128
```

**3. Federated Queries (Cross-Org Sharing)**
```json
{
  "query": {
    "organizations": ["org-123", "org-456"],
    "sharing_agreement_id": "coalition-alpha",
    "filters": {
      "entity_type": "aircraft_multirotor",
      "classification": ["unclassified", "cui"]
    }
  }
}
```

### Event Streaming Architecture

#### Event Types
```yaml
Entity Events:
  - entity.created
  - entity.updated
  - entity.deleted
  - entity.position_changed

Mission Events:
  - mission.created
  - mission.status_changed
  - mission.assigned
  - mission.completed

System Events:
  - system.health_check
  - system.alert
  - system.configuration_changed
```

#### Event Schema
```json
{
  "event_id": "uuid",
  "event_type": "entity.position_changed",
  "timestamp": "2024-01-20T12:00:00Z",
  "org_id": "org-123",
  "entity_id": "drone-001",
  "data": {
    "previous": {...},
    "current": {...}
  },
  "metadata": {
    "source": "gps_sensor",
    "confidence": 0.95
  }
}
```

### Security Architecture

#### Authentication Flow
```
Client → API Gateway → Auth Service → JWT Token
                ↓
         Rate Limiter → Service Router → Backend Service
                               ↓
                        Authorization Check
```

#### Authorization Model
```yaml
Roles:
  org_admin:
    - entities:*
    - missions:*
    - commands:*
    
  mission_commander:
    - entities:read
    - missions:*
    - commands:write
    
  operator:
    - entities:read
    - commands:write:assigned_entities
    
  viewer:
    - entities:read
    - missions:read
```

### Deployment Patterns

#### 1. Kubernetes Deployment
```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: entity-service
spec:
  replicas: 3
  selector:
    matchLabels:
      app: entity-service
  template:
    spec:
      containers:
      - name: entity-service
        image: constellation/entity-service:1.0
        env:
        - name: DB_CONNECTION_POOL_SIZE
          value: "20"
        - name: CACHE_TTL_SECONDS
          value: "300"
        resources:
          requests:
            memory: "256Mi"
            cpu: "250m"
          limits:
            memory: "512Mi"
            cpu: "500m"
```

#### 2. Service Mesh Integration
```yaml
# Istio service mesh for inter-service communication
apiVersion: networking.istio.io/v1beta1
kind: VirtualService
metadata:
  name: entity-service
spec:
  http:
  - match:
    - uri:
        prefix: "/api/v1/*/entities"
    route:
    - destination:
        host: entity-service
        subset: v1
      weight: 90
    - destination:
        host: entity-service
        subset: v2-canary
      weight: 10
```

### Scalability Strategies

#### Horizontal Scaling
- Stateless services scale linearly
- Load balancer distributes requests
- Database connection pooling
- Read replicas for query scaling

#### Caching Layers
```yaml
Cache Strategy:
  L1: Service-level memory cache (5min TTL)
  L2: Redis distributed cache (30min TTL)
  L3: CDN for static resources
```

#### Performance Optimizations
1. **Database Indexes**: Optimized for common query patterns
2. **Query Batching**: GraphQL DataLoader pattern
3. **Async Processing**: Non-blocking I/O for all services
4. **Connection Pooling**: Reuse database/HTTP connections

### API Gateway Features

#### Rate Limiting
```yaml
Tiers:
  basic:
    requests_per_second: 10
    burst: 20
    
  premium:
    requests_per_second: 100
    burst: 200
    
  enterprise:
    requests_per_second: 1000
    burst: 2000
```

#### Request Transformation
```javascript
// Transform legacy TAK requests to modern API
{
  "transform": {
    "request": {
      "headers": {
        "X-Org-ID": "$.cot.uid.split('-')[0]"
      },
      "body": {
        "entity_type": "mapCotType($.cot.type)",
        "position": {
          "latitude": "$.cot.point.lat",
          "longitude": "$.cot.point.lon"
        }
      }
    }
  }
}
```

### Monitoring and Observability

#### Metrics Collection
```yaml
Service Metrics:
  - Request rate
  - Response time (p50, p95, p99)
  - Error rate
  - Active connections
  
Business Metrics:
  - Entities per organization
  - Commands executed
  - Mission completion rate
  - System utilization
```

#### Distributed Tracing
```
Request → API Gateway → Entity Service → Database
   ↓          ↓              ↓            ↓
 TraceID   SpanID-1      SpanID-2    SpanID-3
```

### Disaster Recovery

#### Backup Strategy
1. **Real-time Replication**: Multi-region database replication
2. **Point-in-Time Recovery**: 30-day backup retention
3. **Event Replay**: Rebuild state from event stream

#### Failover Procedures
```yaml
Primary Region Failure:
  1. Health check fails for 3 consecutive attempts
  2. DNS failover to secondary region (30s)
  3. Secondary promotes to primary
  4. New secondary spun up in tertiary region
```

### Integration Patterns

#### Webhook System
```json
{
  "webhook_config": {
    "url": "https://external-system.com/webhook",
    "events": ["entity.created", "mission.completed"],
    "headers": {
      "Authorization": "Bearer <token>"
    },
    "retry_policy": {
      "max_attempts": 3,
      "backoff_multiplier": 2
    }
  }
}
```

#### Batch Processing
```yaml
Batch Jobs:
  - Telemetry aggregation (every 5 min)
  - Entity position history rollup (hourly)
  - Mission performance analytics (daily)
  - Compliance reports (weekly)
```

This architecture provides a robust, scalable foundation for multi-organization common operating picture systems while maintaining security, performance, and operational flexibility.