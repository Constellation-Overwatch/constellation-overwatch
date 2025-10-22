# Constellation Overwatch - Unified Schema and Ontology
## Multi-Organization Common Operating Picture Data Infrastructure

### 1. Core Domain Ontology

#### 1.1 Entity Taxonomy
The system defines a hierarchical entity classification supporting multi-domain operations:

```yaml
Entity:
  ├── Physical Assets
  │   ├── Air Domain
  │   │   ├── aircraft_fixed_wing
  │   │   ├── aircraft_multirotor  
  │   │   ├── aircraft_vtol
  │   │   └── aircraft_helicopter
  │   ├── Ground Domain
  │   │   ├── ground_vehicle_wheeled
  │   │   └── ground_vehicle_tracked
  │   ├── Surface Domain
  │   │   └── surface_vessel_usv
  │   └── Subsurface Domain
  │       └── underwater_vehicle
  │   
  ├── System Components
  │   ├── sensor_platform
  │   ├── payload_system
  │   └── operator_station
  │   
  └── Virtual/Logical Entities
      ├── waypoint
      ├── no_fly_zone
      └── geofence
```

#### 1.2 Core Data Models

##### Entity Schema
```json
{
  "entity": {
    "entity_id": "uuid",
    "entity_type": "enum<EntityType>",
    "status": "enum<active|inactive|pending|error|maintenance|unknown>",
    "priority": "enum<critical|high|normal|low>",
    "timestamp": "iso8601",
    "is_live": "boolean",
    "expiry_time": "iso8601|null",
    "position": {
      "latitude": "float[-90,90]",
      "longitude": "float[-180,180]",
      "altitude": "float",
      "heading": "float[0,360]|null",
      "velocity": "float|null",
      "accuracy": "float|null",
      "timestamp": "iso8601"
    },
    "components": {
      "<component_type>": {
        "type": "string",
        "data": "object",
        "timestamp": "iso8601",
        "version": "string",
        "schema": "object|null"
      }
    },
    "relationships": {
      "<target_id>": {
        "source_id": "uuid",
        "target_id": "uuid",
        "type": "enum<parent_child|attached_to|follows|escorts|commands|monitors>",
        "metadata": "object",
        "timestamp": "iso8601"
      }
    },
    "aliases": "object<string,string>",
    "tags": "array<string>",
    "provenance": {
      "source": "string",
      "created_by": "string",
      "classification": "string|null"
    },
    "metadata": "object"
  }
}
```

##### Message Schema
```json
{
  "message": {
    "message_id": "uuid",
    "message_type": "enum<MessageType>",
    "source": "string",
    "target": "string|null",
    "topic": "string",
    "payload": "object",
    "timestamp": "iso8601",
    "priority": "integer",
    "ttl": "float|null"
  }
}
```

### 2. Communication Patterns

#### 2.1 Message Types
```yaml
Messages:
  Entity Lifecycle:
    - entity_created
    - entity_updated  
    - entity_removed
  
  Mission Management:
    - mission_assigned
    - mission_started
    - mission_completed
    - mission_failed
    
  Vehicle Control:
    - vehicle_status
    - vehicle_command
    - vehicle_telemetry
    
  System Events:
    - system_status
    - system_error
    - system_shutdown
```

#### 2.2 Event Flow Patterns
```
Producer → Message Bus → Consumer(s)
   ↓           ↓            ↓
  Entity    Topic-based  Filtered
  Manager   Routing     Delivery
```

### 3. Spatial Data Infrastructure

#### 3.1 Spatial Indexing
- Geospatial queries using haversine distance calculations
- Radius-based entity searches
- Support for 3D positioning (lat/lon/alt)

#### 3.2 Coordinate Systems
- WGS84 datum for all geographic coordinates
- Altitude in meters above mean sea level (MSL)
- Heading in degrees (0-360, clockwise from north)

### 4. Interoperability Standards

#### 4.1 TAK/CoT Integration
Mapping between Constellation entities and Cursor on Target (CoT) types:

```yaml
Entity Mappings:
  aircraft_multirotor: "a-f-A-M-F-Q"  # Friendly Air MQ Drone
  ground_vehicle_*: "a-f-G-U-C"       # Friendly Ground Vehicle  
  sensor_detections: "a-u-G"          # Unknown Ground Contact
  waypoint: "b-m-p-w"                 # Route Waypoint
  geofence: "u-d-f"                   # Drawing/Shape
```

#### 4.2 Protocol Support
- REST API (HTTP/HTTPS)
- WebSocket (real-time streaming)
- TAK Protocol v0/v1 (XML/Protobuf CoT)
- Future: MAVLink, ROS 2, DDS

### 5. Stateless API Service Architecture

#### 5.1 Design Principles
1. **Stateless Operations**: All API calls contain complete context
2. **Event Sourcing**: State changes captured as immutable events
3. **CQRS Pattern**: Separate read/write models for scalability
4. **Multi-tenancy**: Organization-based data isolation

#### 5.2 Core API Services

##### Entity Service
```yaml
/api/v1/entities:
  GET:    Query entities with filters
  POST:   Create new entity
  
/api/v1/entities/{id}:
  GET:    Retrieve specific entity
  PUT:    Update entity
  DELETE: Remove entity
  
/api/v1/entities/spatial:
  POST:   Spatial query (center point + radius)
  
/api/v1/entities/{id}/relationships:
  GET:    Get entity relationships
  POST:   Create relationship
```

##### Command Service  
```yaml
/api/v1/vehicles/{id}/commands:
  POST:   Send vehicle command
  
/api/v1/missions:
  POST:   Create mission
  PUT:    Update mission status
```

##### Streaming Service
```yaml
/api/v1/stream/entities:
  WebSocket: Real-time entity updates
  
/api/v1/stream/telemetry:
  WebSocket: Live telemetry feed
```

#### 5.3 Multi-Organization Architecture

##### Organization Context
```json
{
  "organization": {
    "org_id": "uuid",
    "name": "string",
    "type": "enum<military|civilian|commercial|ngo>",
    "classification_levels": ["unclassified", "cui", "secret"],
    "data_sharing_agreements": ["org_id_1", "org_id_2"]
  }
}
```

##### Data Partitioning Strategy
1. **Logical Isolation**: Organization ID in all entities
2. **Physical Isolation**: Separate data stores per classification
3. **Cross-Domain Sharing**: Explicit sharing agreements
4. **Federated Queries**: Cross-organization data aggregation

### 6. Security and Access Control

#### 6.1 Authentication
- Certificate-based (X.509 PKI)
- OAuth 2.0 / OpenID Connect
- API key authentication

#### 6.2 Authorization Model
```yaml
Roles:
  viewer:    Read-only access
  operator:  Control individual assets
  commander: Mission planning and coordination
  admin:     Full system configuration
  
Permissions:
  entity:read
  entity:write
  entity:delete
  command:send
  mission:create
  mission:modify
```

### 7. Performance and Scalability

#### 7.1 Caching Strategy
- Entity cache with TTL-based expiration
- Spatial query result caching
- WebSocket connection pooling

#### 7.2 Horizontal Scaling
- Stateless services enable load balancing
- Message bus supports multiple consumers
- Database sharding by organization/region

### 8. Data Persistence

#### 8.1 Storage Patterns
```yaml
Operational Store:
  - In-memory entity cache
  - Redis for session state
  - Message queue persistence
  
Analytical Store:
  - Time-series telemetry data
  - Historical entity positions
  - Mission execution logs
  
Archive Store:
  - Compliance/audit trails
  - Long-term mission records
```

### 9. Integration Patterns

#### 9.1 Plugin Architecture
- Dynamic plugin loading
- Standardized interfaces (Vehicle, Sensor, AI, Communication)
- Event-driven plugin lifecycle

#### 9.2 External System Integration
- MAVLink for drone control
- ROS 2 for robotics
- TAK for situational awareness
- Cloud services (AWS, Azure, GCP)

### 10. Deployment Architecture

#### 10.1 Microservices Deployment
```yaml
Services:
  entity-service:
    replicas: 3
    endpoints: [/entities]
    
  command-service:
    replicas: 2
    endpoints: [/vehicles/*/commands, /missions]
    
  stream-service:
    replicas: 4
    endpoints: [/stream/*]
    
  tak-gateway:
    replicas: 2
    protocols: [CoT, TAK v0/v1]
```

#### 10.2 Container Orchestration
- Kubernetes-ready with Helm charts
- Docker Compose for development
- Support for air-gapped deployments

This schema and ontology provides the foundation for building stateless API services that support multi-organization common operating picture requirements while maintaining security, scalability, and interoperability.