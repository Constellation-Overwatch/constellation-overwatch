# PRD: Alien - NATS Client for Open Mothership

## Overview

Alien is a Python-based NATS client designed to connect edge devices (drones, robots, sensors) to the Open Mothership C2 platform. It handles automatic organization registration, secure communication, and provides a simple API for publishing telemetry and receiving commands.

## Goals

1. **Zero-Configuration Setup** - Automatically register with mothership on first run
2. **Persistent Identity** - Save organization and entity IDs locally for reuse
3. **Reliable Communication** - Use NATS JetStream for guaranteed message delivery
4. **Simple API** - Easy-to-use methods for common operations
5. **Resilient Operation** - Handle network interruptions gracefully

## Technical Architecture

### Dependencies

```toml
[project]
name = "alien"
version = "0.1.0"
description = "NATS client for Open Mothership edge devices"
dependencies = [
    "nats-py>=2.6.0",
    "aiohttp>=3.9.0",
    "pydantic>=2.5.0",
    "python-dotenv>=1.0.0",
    "asyncio>=3.4.3",
]
```

### Core Components

#### 1. Configuration Management (`config.py`)

```python
from pydantic import BaseSettings, Field
from typing import Optional
import json
import os

class AlienConfig(BaseSettings):
    # Mothership connection
    mothership_url: str = Field(default="http://localhost:8080", env="MOTHERSHIP_URL")
    nats_url: str = Field(default="nats://localhost:4222", env="NATS_URL")
    bearer_token: str = Field(default="constellation-dev-token", env="API_BEARER_TOKEN")
    
    # Organization settings
    org_name: str = Field(default="Alien Fleet", env="ORG_NAME")
    org_type: str = Field(default="commercial", env="ORG_TYPE")
    
    # Entity settings
    entity_type: str = Field(default="drone", env="ENTITY_TYPE")
    entity_name: Optional[str] = Field(default=None, env="ENTITY_NAME")
    
    # Persistence
    config_file: str = Field(default=".alien_config.json", env="ALIEN_CONFIG_FILE")
    
    # Saved state
    org_id: Optional[str] = None
    entity_id: Optional[str] = None
    
    class Config:
        env_file = ".env"
        env_file_encoding = 'utf-8'
    
    def save(self):
        """Save current configuration to file"""
        data = {
            "org_id": self.org_id,
            "entity_id": self.entity_id,
            "org_name": self.org_name,
            "entity_type": self.entity_type,
            "entity_name": self.entity_name
        }
        with open(self.config_file, 'w') as f:
            json.dump(data, f, indent=2)
    
    def load(self):
        """Load configuration from file if exists"""
        if os.path.exists(self.config_file):
            with open(self.config_file, 'r') as f:
                data = json.load(f)
                self.org_id = data.get("org_id")
                self.entity_id = data.get("entity_id")
```

#### 2. Mothership Client (`mothership.py`)

```python
import aiohttp
import asyncio
from typing import Dict, Any, Optional
import logging

logger = logging.getLogger(__name__)

class MothershipClient:
    def __init__(self, config: AlienConfig):
        self.config = config
        self.session: Optional[aiohttp.ClientSession] = None
    
    async def __aenter__(self):
        self.session = aiohttp.ClientSession(
            headers={"Authorization": f"Bearer {self.config.bearer_token}"}
        )
        return self
    
    async def __aexit__(self, exc_type, exc_val, exc_tb):
        if self.session:
            await self.session.close()
    
    async def create_organization(self) -> Dict[str, Any]:
        """Create organization if not exists"""
        url = f"{self.config.mothership_url}/api/v1/organizations"
        data = {
            "name": self.config.org_name,
            "org_type": self.config.org_type
        }
        
        async with self.session.post(url, json=data) as resp:
            result = await resp.json()
            if resp.status == 201 and result.get("success"):
                return result["data"]
            else:
                raise Exception(f"Failed to create organization: {result}")
    
    async def create_entity(self, org_id: str, position: Optional[Dict] = None) -> Dict[str, Any]:
        """Create entity in organization"""
        url = f"{self.config.mothership_url}/api/v1/entities?org_id={org_id}"
        data = {
            "entity_type": self.config.entity_type,
            "status": "active",
            "priority": "normal",
            "metadata": {
                "name": self.config.entity_name or f"alien-{self.config.entity_type}",
                "client": "alien-v0.1.0"
            }
        }
        
        if position:
            data["position"] = position
        
        async with self.session.post(url, json=data) as resp:
            result = await resp.json()
            if resp.status == 201 and result.get("success"):
                return result["data"]
            else:
                raise Exception(f"Failed to create entity: {result}")
    
    async def update_entity_status(self, org_id: str, entity_id: str, status: str) -> Dict[str, Any]:
        """Update entity status"""
        url = f"{self.config.mothership_url}/api/v1/entities?org_id={org_id}&entity_id={entity_id}"
        data = {"status": status}
        
        async with self.session.put(url, json=data) as resp:
            result = await resp.json()
            if resp.status == 200 and result.get("success"):
                return result["data"]
            else:
                raise Exception(f"Failed to update entity: {result}")
```

#### 3. NATS Client (`client.py`)

```python
import nats
from nats.errors import TimeoutError
import asyncio
import json
import logging
from datetime import datetime
from typing import Dict, Any, Optional, Callable
import uuid

logger = logging.getLogger(__name__)

class AlienClient:
    def __init__(self, config: AlienConfig):
        self.config = config
        self.nc: Optional[nats.NATS] = None
        self.js: Optional[nats.JetStreamContext] = None
        self.mothership: Optional[MothershipClient] = None
        self._command_handlers: Dict[str, Callable] = {}
        self._running = False
    
    async def initialize(self):
        """Initialize connection and register with mothership"""
        # Load saved configuration
        self.config.load()
        
        # Initialize mothership client
        self.mothership = MothershipClient(self.config)
        
        # Register with mothership if needed
        async with self.mothership:
            if not self.config.org_id:
                logger.info("Creating new organization...")
                org = await self.mothership.create_organization()
                self.config.org_id = org["org_id"]
                logger.info(f"Created organization: {self.config.org_id}")
            
            if not self.config.entity_id:
                logger.info("Creating new entity...")
                entity = await self.mothership.create_entity(self.config.org_id)
                self.config.entity_id = entity["entity_id"]
                logger.info(f"Created entity: {self.config.entity_id}")
            
            # Save configuration
            self.config.save()
        
        # Connect to NATS
        await self._connect_nats()
    
    async def _connect_nats(self):
        """Connect to NATS server"""
        self.nc = await nats.connect(
            self.config.nats_url,
            error_cb=self._error_cb,
            disconnected_cb=self._disconnected_cb,
            reconnected_cb=self._reconnected_cb,
        )
        
        # Get JetStream context
        self.js = self.nc.jetstream()
        logger.info(f"Connected to NATS at {self.config.nats_url}")
        
        # Subscribe to commands
        await self._subscribe_commands()
    
    async def _subscribe_commands(self):
        """Subscribe to entity-specific commands"""
        subject = f"constellation.commands.{self.config.org_id}.{self.config.entity_id}"
        
        async def command_handler(msg):
            try:
                data = json.loads(msg.data.decode())
                command_type = data.get("type")
                
                if command_type in self._command_handlers:
                    await self._command_handlers[command_type](data)
                else:
                    logger.warning(f"No handler for command type: {command_type}")
                
                await msg.ack()
            except Exception as e:
                logger.error(f"Error processing command: {e}")
        
        # Create durable consumer for commands
        await self.js.subscribe(
            subject,
            cb=command_handler,
            durable=f"alien-{self.config.entity_id}",
            manual_ack=True
        )
        logger.info(f"Subscribed to commands on {subject}")
    
    def register_command_handler(self, command_type: str, handler: Callable):
        """Register a handler for specific command types"""
        self._command_handlers[command_type] = handler
    
    async def publish_telemetry(self, data: Dict[str, Any]):
        """Publish telemetry data"""
        subject = f"constellation.telemetry.{self.config.org_id}.{self.config.entity_id}"
        
        message = {
            "entity_id": self.config.entity_id,
            "timestamp": datetime.utcnow().isoformat(),
            "data": data
        }
        
        msg_id = f"{self.config.entity_id}-{datetime.utcnow().timestamp()}"
        
        await self.js.publish(
            subject,
            json.dumps(message).encode(),
            headers={"Nats-Msg-Id": msg_id}
        )
    
    async def publish_event(self, event_type: str, data: Dict[str, Any]):
        """Publish an event"""
        subject = f"constellation.events.{self.config.org_id}.{event_type}"
        
        message = {
            "entity_id": self.config.entity_id,
            "event_type": event_type,
            "timestamp": datetime.utcnow().isoformat(),
            "data": data
        }
        
        msg_id = str(uuid.uuid4())
        
        await self.js.publish(
            subject,
            json.dumps(message).encode(),
            headers={"Nats-Msg-Id": msg_id}
        )
    
    async def update_position(self, latitude: float, longitude: float, altitude: float = 0):
        """Update entity position"""
        data = {
            "position": {
                "latitude": latitude,
                "longitude": longitude,
                "altitude": altitude
            }
        }
        await self.publish_telemetry(data)
    
    async def update_status(self, status: str):
        """Update entity status"""
        async with self.mothership:
            await self.mothership.update_entity_status(
                self.config.org_id,
                self.config.entity_id,
                status
            )
        
        await self.publish_event("status_changed", {"status": status})
    
    async def start(self):
        """Start the client (for long-running operations)"""
        self._running = True
        try:
            while self._running:
                await asyncio.sleep(1)
        except KeyboardInterrupt:
            logger.info("Shutting down...")
        finally:
            await self.close()
    
    async def close(self):
        """Close all connections"""
        self._running = False
        if self.nc:
            await self.nc.close()
    
    # Callbacks
    async def _error_cb(self, e):
        logger.error(f"NATS error: {e}")
    
    async def _disconnected_cb(self):
        logger.warning("Disconnected from NATS")
    
    async def _reconnected_cb(self):
        logger.info("Reconnected to NATS")
```

#### 4. Example Usage (`example.py`)

```python
import asyncio
import logging
from alien import AlienClient, AlienConfig
import random

logging.basicConfig(level=logging.INFO)

async def main():
    # Initialize client with custom configuration
    config = AlienConfig(
        org_name="Drone Squadron Alpha",
        entity_type="drone",
        entity_name="HAWK-1"
    )
    
    client = AlienClient(config)
    
    # Initialize and connect
    await client.initialize()
    
    # Register command handlers
    async def handle_return_to_base(data):
        print("Received return to base command!")
        await client.update_status("returning")
    
    client.register_command_handler("return_to_base", handle_return_to_base)
    
    # Simulate telemetry updates
    try:
        await client.update_status("active")
        
        for i in range(100):
            # Simulate GPS updates
            lat = 40.7128 + random.uniform(-0.01, 0.01)
            lon = -74.0060 + random.uniform(-0.01, 0.01)
            alt = 100 + random.uniform(-10, 10)
            
            await client.update_position(lat, lon, alt)
            
            # Publish other telemetry
            await client.publish_telemetry({
                "battery_level": 85 - (i * 0.1),
                "speed": random.uniform(5, 15),
                "heading": random.uniform(0, 360),
                "sensors": {
                    "temperature": 20 + random.uniform(-2, 2),
                    "pressure": 1013.25 + random.uniform(-5, 5)
                }
            })
            
            await asyncio.sleep(1)
            
    finally:
        await client.update_status("inactive")
        await client.close()

if __name__ == "__main__":
    asyncio.run(main())
```

#### 5. Package Structure

```
alien/
├── pyproject.toml
├── README.md
├── .env.example
├── src/
│   └── alien/
│       ├── __init__.py
│       ├── config.py
│       ├── mothership.py
│       ├── client.py
│       └── __main__.py
├── examples/
│   ├── basic_telemetry.py
│   ├── command_handler.py
│   └── fleet_simulation.py
└── tests/
    ├── __init__.py
    ├── test_config.py
    ├── test_mothership.py
    └── test_client.py
```

#### 6. Environment Variables (.env.example)

```bash
# Mothership Configuration
MOTHERSHIP_URL=http://localhost:8080
NATS_URL=nats://localhost:4222
API_BEARER_TOKEN=constellation-dev-token

# Organization Settings
ORG_NAME=My Drone Fleet
ORG_TYPE=commercial

# Entity Settings
ENTITY_TYPE=drone
ENTITY_NAME=DRONE-001

# Persistence
ALIEN_CONFIG_FILE=.alien_config.json
```

## Key Features

1. **Auto-Registration**: Automatically creates organization and entity on first run
2. **State Persistence**: Saves org_id and entity_id locally for reuse
3. **Resilient Connections**: Handles NATS disconnections/reconnections
4. **Command Processing**: Subscribe to entity-specific commands
5. **Telemetry Publishing**: Easy methods for publishing position and sensor data
6. **Event System**: Publish status changes and custom events
7. **Message Deduplication**: Uses Nats-Msg-Id for exactly-once semantics

## Security Considerations

1. Store API bearer token securely (use environment variables)
2. Implement TLS for production NATS connections
3. Validate all incoming commands
4. Rate limit telemetry publishing to prevent flooding
5. Implement message signing for critical commands

## Future Enhancements

1. **Batch Telemetry**: Collect and send telemetry in batches
2. **Offline Mode**: Queue messages when disconnected
3. **Metrics Collection**: Track message rates and latencies
4. **Video Streaming**: Support for video stream metadata
5. **Multi-Entity Support**: Single client managing multiple entities
6. **Firmware Updates**: Support OTA updates via NATS
7. **Encryption**: End-to-end encryption for sensitive data

## Usage Examples

### Basic Drone Client

```python
async def drone_main():
    config = AlienConfig(entity_type="drone")
    client = AlienClient(config)
    await client.initialize()
    
    # Main loop
    while True:
        gps = read_gps()
        await client.update_position(gps.lat, gps.lon, gps.alt)
        await asyncio.sleep(0.1)
```

### Sensor Platform

```python
async def sensor_main():
    config = AlienConfig(entity_type="sensor", entity_name="weather-01")
    client = AlienClient(config)
    await client.initialize()
    
    while True:
        data = read_sensors()
        await client.publish_telemetry(data)
        await asyncio.sleep(60)  # Every minute
```

### Command-Responsive Robot

```python
async def robot_main():
    config = AlienConfig(entity_type="robot")
    client = AlienClient(config)
    
    # Register command handlers
    client.register_command_handler("move", handle_move)
    client.register_command_handler("stop", handle_stop)
    client.register_command_handler("patrol", handle_patrol)
    
    await client.initialize()
    await client.start()  # Runs until interrupted
```