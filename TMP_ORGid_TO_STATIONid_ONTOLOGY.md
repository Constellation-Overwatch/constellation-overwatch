# Video Path Structure: org_id/entity_id

## Overview

Updated video streaming infrastructure to use MediaMTX WebRTC with path structure:
```
/{org_id}/{entity_id}
```

Where:
- `org_id` - UUID from database (will become station_id for ground/air/water/space stations)
- `entity_id` - Entity UUID

## Changes Made

### 1. Configuration (.env.example)

Added MediaMTX configuration:
```bash
MEDIAMTX_WEBRTC_URL=http://localhost:8889
MEDIAMTX_API_URL=http://localhost:9997
```

Disabled NATS WebSocket (no longer needed):
```bash
NATS_WS_PORT=0
```

### 2. Video Templates

#### pkg/services/web/features/video/pages/video.templ

- `VideoPage(entityIDs, mediaMTXURL)` - passes MediaMTX URL
- `VideoPanel(entityIDs, mediaMTXURL)` - WebRTC iframe with dynamic path
- `VideoCard(entity, mediaMTXURL)` - iframe src: `{mediaMTXURL}/{org_id}/{entity_id}`
- `VideoPageStyles(mediaMTXURL)` - JS fullscreen uses `openVideoFullscreen(stationId, entityId)`

#### pkg/services/web/features/common/components/c4_entity_card.templ

- `C4VideoPlayer(stationID, entityID, mediaMTXURL)` - 3 params now
- `C4EntityCardScript(mediaMTXURL)` - stores URL globally, fullscreen uses station/entity path

#### pkg/services/web/features/map/components/map_panel.templ

- `MapPanel(mediaMTXURL)` - passes URL to C4EntityCardScript

#### pkg/services/web/features/map/pages/map.templ

- `MapPage(mediaMTXURL)` - passes URL to MapPanel

### 3. Handlers

#### pkg/services/web/handlers/pages.go

- `HandleVideoPage` - gets `MEDIAMTX_WEBRTC_URL` env, passes to VideoPage
- `HandleMapPage` - gets `MEDIAMTX_WEBRTC_URL` env, passes to MapPage

#### pkg/services/web/handlers/video.go

- Added `getMediaMTXURL()` helper
- `HandleAPIVideoList` - extracts `OrgID` from KV store for video path

#### pkg/services/web/handlers/overwatch.go

- Added `getMediaMTXURLOverwatch()` helper
- `C4VideoPlayer(entityState.OrgID, entityID, mediaMTXURL)` - passes org_id

### 4. NATS Configuration

#### pkg/services/embedded-nats/nats.go

- WebSocket only enabled if `WSPort > 0`
- Set `NATS_WS_PORT=0` to disable

### 5. Docker/Deployment

#### docker-compose.yml
- Removed port 8222
- Set `NATS_WS_PORT=0`
- Updated healthcheck to use `/health`

#### Dockerfile
- Removed 8222 from EXPOSE

## Video Path Examples

```
MediaMTX WebRTC URL: http://localhost:8889

Org ID:    550e8400-e29b-41d4-a716-446655440000
Entity ID: drone-alpha-1

Full Path: http://localhost:8889/550e8400-e29b-41d4-a716-446655440000/drone-alpha-1
```

## Integration with video-publisher / gocv2constellation

Both services use the same path structure:

```bash
# Publisher (RTSP)
rtsp://publisher:gcs-publish-secret@172.28.0.40:8554/{ORG_ID}/{ENTITY_ID}

# Viewer (WebRTC in browser)
http://localhost:8889/{ORG_ID}/{ENTITY_ID}

# Environment
STATION_ID=550e8400-e29b-41d4-a716-446655440000  # Same as ORG_ID
ENTITY_ID=drone-alpha-1
```

## Future: org_id -> station_id Migration

The `org_id` field will transition to `station_id` representing:
- Ground stations
- Air stations
- Water stations
- Space stations

These are edge nexus points commanding fleets, geotagged with live state and documents.

The code uses `entity.OrgID` which will map to the station UUID when the ontology changes.

## Files Modified

```
.env.example
cmd/microlith/main.go
docker-compose.yml
Dockerfile
pkg/services/embedded-nats/nats.go
pkg/services/web/features/common/components/c4_entity_card.templ
pkg/services/web/features/map/components/map_panel.templ
pkg/services/web/features/map/pages/map.templ
pkg/services/web/features/video/pages/video.templ
pkg/services/web/handlers/overwatch.go
pkg/services/web/handlers/pages.go
pkg/services/web/handlers/video.go
```
