package shared

import "fmt"

// NATS Subject patterns
const (
	// Base subject prefixes
	SubjectPrefix = "constellation"

	// Entity subjects
	SubjectEntities        = "constellation.entities"
	SubjectEntitiesAll     = "constellation.entities.>"
	SubjectEntityCreated   = "constellation.entities.%s.created"   // org_id
	SubjectEntityUpdated   = "constellation.entities.%s.updated"   // org_id
	SubjectEntityDeleted   = "constellation.entities.%s.deleted"   // org_id
	SubjectEntityStatus    = "constellation.entities.%s.status"    // org_id
	SubjectEntityTelemetry = "constellation.entities.%s.telemetry" // org_id

	// Organization subjects
	SubjectOrganizations    = "constellation.organizations"
	SubjectOrganizationsAll = "constellation.organizations.>"
	SubjectOrgCreated       = "constellation.organizations.created"
	SubjectOrgUpdated       = "constellation.organizations.updated"
	SubjectOrgDeleted       = "constellation.organizations.deleted"

	// Event subjects
	SubjectEvents    = "constellation.events"
	SubjectEventsAll = "constellation.events.>"

	// Telemetry subjects
	SubjectTelemetry       = "constellation.telemetry"
	SubjectTelemetryAll    = "constellation.telemetry.>"
	SubjectTelemetryEntity = "constellation.telemetry.%s.%s" // org_id, entity_id

	// Command subjects
	SubjectCommands              = "constellation.commands"
	SubjectCommandsAll           = "constellation.commands.>"
	SubjectCommandEntity         = "constellation.commands.%s.%s"              // org_id, entity_id
	SubjectCommandBroadcast      = "constellation.commands.%s.broadcast"       // org_id
	SubjectCommandAgentOpsLaunch = "constellation.commands.%s.agentops.launch" // org_id

	// Agent operations subjects
	SubjectAgentOps         = "constellation.agentops"
	SubjectAgentOpsAll      = "constellation.agentops.>"
	SubjectAgentOpsEvents   = "constellation.agentops.%s.%s"         // node_id, event_type
	SubjectAgentOpsTools    = "constellation.agentops.%s.tool.%s"    // node_id, tool_name
	SubjectAgentOpsSessions = "constellation.agentops.%s.session.%s" // node_id, provider

	// System subjects
	SubjectSystemHealth  = "constellation.system.health"
	SubjectSystemMetrics = "constellation.system.metrics"
	SubjectSystemAlerts  = "constellation.system.alerts"
)

// Stream names
const (
	StreamEntities  = "CONSTELLATION_ENTITIES"
	StreamEvents    = "CONSTELLATION_EVENTS"
	StreamTelemetry = "CONSTELLATION_TELEMETRY"
	StreamCommands  = "CONSTELLATION_COMMANDS"
	StreamAgentOps  = "CONSTELLATION_AGENTOPS"
)

// Consumer names
const (
	ConsumerEntityProcessor    = "entity-processor"
	ConsumerEventProcessor     = "event-processor"
	ConsumerCommandProcessor   = "command-processor"
	ConsumerTelemetryProcessor = "telemetry-processor"
	ConsumerAgentOpsProcessor  = "agentops-processor"
)

// KV Bucket names
const (
	KVBucketGlobalState = "CONSTELLATION_GLOBAL_STATE"
)

// Object Store bucket names
const (
	ObjectStoreBucketConstellation = "CONSTELLATION_OBJECTS"
)

// KV Key patterns for global state
const (
	KVKeyEntity      = "entity:%s"       // entity_id -> full entity state
	KVKeyFleet       = "fleet:%s"        // fleet_id
	KVKeySwarm       = "swarm:%s"        // swarm_id
	KVKeyEntityList  = "entities:list"   // List of all entity IDs
	KVKeyFleetList   = "fleets:list"     // List of all fleet IDs
	KVKeySwarmList   = "swarms:list"     // List of all swarm IDs
	KVKeyFleetSwarms = "fleet:%s:swarms" // fleet_id -> list of swarm IDs
	KVKeySwarmFleet  = "swarm:%s:fleet"  // swarm_id -> fleet_id
	KVKeyOrgEntities = "org:%s:entities" // org_id -> list of entity IDs
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

func CommandAgentOpsLaunchSubject(orgID string) string {
	if orgID == "" {
		orgID = "default"
	}
	return fmt.Sprintf(SubjectCommandAgentOpsLaunch, orgID)
}

func AgentOpsEventSubject(nodeID, eventType string) string {
	if nodeID == "" {
		nodeID = "unknown"
	}
	if eventType == "" {
		eventType = "observed"
	}
	return fmt.Sprintf(SubjectAgentOpsEvents, nodeID, eventType)
}

func AgentOpsToolSubject(nodeID, toolName string) string {
	if nodeID == "" {
		nodeID = "unknown"
	}
	if toolName == "" {
		toolName = "unknown"
	}
	return fmt.Sprintf(SubjectAgentOpsTools, nodeID, toolName)
}

func AgentOpsSessionSubject(nodeID, provider string) string {
	if nodeID == "" {
		nodeID = "unknown"
	}
	if provider == "" {
		provider = "unknown"
	}
	return fmt.Sprintf(SubjectAgentOpsSessions, nodeID, provider)
}

func OrgCreatedSubject() string {
	return SubjectOrgCreated
}

func OrgUpdatedSubject() string {
	return SubjectOrgUpdated
}

func OrgDeletedSubject() string {
	return SubjectOrgDeleted
}

// Helper functions to generate KV keys
func EntityKey(entityID string) string {
	// Return just the entity_id as the key (not "entity:{id}" format)
	return entityID
}

func FleetKey(fleetID string) string {
	return fmt.Sprintf(KVKeyFleet, fleetID)
}

func SwarmKey(swarmID string) string {
	return fmt.Sprintf(KVKeySwarm, swarmID)
}

func FleetSwarmsKey(fleetID string) string {
	return fmt.Sprintf(KVKeyFleetSwarms, fleetID)
}

func SwarmFleetKey(swarmID string) string {
	return fmt.Sprintf(KVKeySwarmFleet, swarmID)
}

func OrgEntitiesKey(orgID string) string {
	return fmt.Sprintf(KVKeyOrgEntities, orgID)
}
