package shared

import "fmt"

// NATS Subject patterns
const (
	// Base subject prefixes
	SubjectPrefix = "constellation"
	
	// Entity subjects
	SubjectEntities         = "constellation.entities"
	SubjectEntitiesAll      = "constellation.entities.>"
	SubjectEntityCreated    = "constellation.entities.%s.created"    // org_id
	SubjectEntityUpdated    = "constellation.entities.%s.updated"    // org_id
	SubjectEntityDeleted    = "constellation.entities.%s.deleted"    // org_id
	SubjectEntityStatus     = "constellation.entities.%s.status"     // org_id
	SubjectEntityTelemetry  = "constellation.entities.%s.telemetry"  // org_id
	
	// Event subjects
	SubjectEvents    = "constellation.events"
	SubjectEventsAll = "constellation.events.>"
	
	// Telemetry subjects
	SubjectTelemetry        = "constellation.telemetry"
	SubjectTelemetryAll     = "constellation.telemetry.>"
	SubjectTelemetryEntity  = "constellation.telemetry.%s.%s" // org_id, entity_id
	
	// Command subjects
	SubjectCommands       = "constellation.commands"
	SubjectCommandsAll    = "constellation.commands.>"
	SubjectCommandEntity  = "constellation.commands.%s.%s" // org_id, entity_id
	SubjectCommandBroadcast = "constellation.commands.%s.broadcast" // org_id
	
	// System subjects
	SubjectSystemHealth   = "constellation.system.health"
	SubjectSystemMetrics  = "constellation.system.metrics"
	SubjectSystemAlerts   = "constellation.system.alerts"
)

// Stream names
const (
	StreamEntities  = "CONSTELLATION_ENTITIES"
	StreamEvents    = "CONSTELLATION_EVENTS"
	StreamTelemetry = "CONSTELLATION_TELEMETRY"
	StreamCommands  = "CONSTELLATION_COMMANDS"
)

// Consumer names
const (
	ConsumerEntityProcessor  = "entity-processor"
	ConsumerEventProcessor   = "event-processor"
	ConsumerCommandProcessor = "command-processor"
	ConsumerTelemetryProcessor = "telemetry-processor"
)

// Helper functions to generate subjects
func EntityCreatedSubject(orgID string) string {
	return fmt.Sprintf(SubjectEntityCreated, orgID)
}

func EntityUpdatedSubject(orgID string) string {
	return fmt.Sprintf(SubjectEntityUpdated, orgID)
}

func EntityDeletedSubject(orgID string) string {
	return fmt.Sprintf(SubjectEntityDeleted, orgID)
}

func EntityStatusSubject(orgID string) string {
	return fmt.Sprintf(SubjectEntityStatus, orgID)
}

func EntityTelemetrySubject(orgID string) string {
	return fmt.Sprintf(SubjectEntityTelemetry, orgID)
}

func TelemetryEntitySubject(orgID, entityID string) string {
	return fmt.Sprintf(SubjectTelemetryEntity, orgID, entityID)
}

func CommandEntitySubject(orgID, entityID string) string {
	return fmt.Sprintf(SubjectCommandEntity, orgID, entityID)
}

func CommandBroadcastSubject(orgID string) string {
	return fmt.Sprintf(SubjectCommandBroadcast, orgID)
}