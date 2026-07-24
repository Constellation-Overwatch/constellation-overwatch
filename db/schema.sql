-- Constellation Overwatch - SQLite Schema
-- Multi-Organization Common Operating Picture Database
-- Converted from PostgreSQL schema



-- ============================================================================
-- CORE TABLES
-- ============================================================================

-- Organizations table (multi-tenancy)
CREATE TABLE organizations (
  org_id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  org_type TEXT NOT NULL CHECK(org_type IN ('military', 'civilian', 'commercial', 'ngo')),
  description TEXT DEFAULT '',
  classification_levels TEXT DEFAULT 'unclassified',
  data_sharing_agreements TEXT DEFAULT '',
  metadata TEXT DEFAULT '{}',
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Entities table (core operational entities)
CREATE TABLE entities (
  entity_id TEXT PRIMARY KEY,
  org_id TEXT NOT NULL,
  name TEXT,
  entity_type TEXT NOT NULL CHECK(entity_type IN (
    'aircraft_fixed_wing', 'aircraft_multirotor', 'aircraft_vtol', 'aircraft_helicopter', 'aircraft_airship',
    'ground_vehicle_wheeled', 'ground_vehicle_tracked',
    'surface_vessel_usv', 'underwater_vehicle',
    'sensor_platform', 'payload_system', 'operator_station',
    'waypoint', 'no_fly_zone', 'geofence'
  )),
  status TEXT NOT NULL DEFAULT 'unknown' CHECK(status IN ('active', 'inactive', 'pending', 'error', 'maintenance', 'unknown')),
  priority TEXT NOT NULL DEFAULT 'normal' CHECK(priority IN ('critical', 'high', 'normal', 'low')),
  is_live INTEGER NOT NULL DEFAULT 0,
  expiry_time TEXT,

  latitude REAL CHECK (latitude >= -90 AND latitude <= 90),
  longitude REAL CHECK (longitude >= -180 AND longitude <= 180),
  altitude REAL,
  heading REAL CHECK (heading >= 0 AND heading <= 360),
  velocity REAL,
  accuracy REAL,
  position_timestamp TEXT,

  components TEXT DEFAULT '{}',
  aliases TEXT DEFAULT '{}',
  tags TEXT DEFAULT '',
  
  source TEXT,
  created_by TEXT,
  classification TEXT,

  metadata TEXT DEFAULT '{}',
  video_config TEXT DEFAULT '{}',

  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at TEXT NOT NULL DEFAULT (datetime('now')),

  FOREIGN KEY (org_id) REFERENCES organizations(org_id) ON DELETE CASCADE
);

-- Entity relationships (graph connections)
CREATE TABLE entity_relationships (
  relationship_id TEXT PRIMARY KEY,
  source_id TEXT NOT NULL,
  target_id TEXT NOT NULL,
  relationship_type TEXT NOT NULL CHECK(relationship_type IN (
    'parent_child', 'attached_to', 'follows', 'escorts', 'commands', 'monitors'
  )),
  metadata TEXT DEFAULT '{}',
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at TEXT NOT NULL DEFAULT (datetime('now')),

  FOREIGN KEY (source_id) REFERENCES entities(entity_id) ON DELETE CASCADE,
  FOREIGN KEY (target_id) REFERENCES entities(entity_id) ON DELETE CASCADE,
  CHECK (source_id != target_id),
  UNIQUE (source_id, target_id, relationship_type)
);

-- Messages table (event sourcing)
CREATE TABLE messages (
  message_id TEXT PRIMARY KEY,
  org_id TEXT NOT NULL,
  message_type TEXT NOT NULL CHECK(message_type IN (
    'entity_created', 'entity_updated', 'entity_removed',
    'mission_assigned', 'mission_started', 'mission_completed', 'mission_failed',
    'vehicle_status', 'vehicle_command', 'vehicle_telemetry',
    'system_status', 'system_error', 'system_shutdown'
  )),
  source TEXT NOT NULL,
  target TEXT,
  topic TEXT NOT NULL,
  payload TEXT NOT NULL,
  priority INTEGER NOT NULL DEFAULT 0,
  ttl REAL,
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  expires_at TEXT,

  FOREIGN KEY (org_id) REFERENCES organizations(org_id) ON DELETE CASCADE,
  CHECK (ttl IS NULL OR ttl > 0)
);

-- Missions table
CREATE TABLE missions (
  mission_id TEXT PRIMARY KEY,
  org_id TEXT NOT NULL,
  name TEXT NOT NULL,
  description TEXT,
  status TEXT NOT NULL DEFAULT 'pending',
  assigned_entities TEXT DEFAULT '',
  waypoints TEXT DEFAULT '[]',
  geofences TEXT DEFAULT '[]',
  metadata TEXT DEFAULT '{}',
  scheduled_start TEXT,
  scheduled_end TEXT,
  actual_start TEXT,
  actual_end TEXT,
  created_by TEXT,
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at TEXT NOT NULL DEFAULT (datetime('now')),

  FOREIGN KEY (org_id) REFERENCES organizations(org_id) ON DELETE CASCADE
);

-- Users table (authentication and authorization)
CREATE TABLE users (
  user_id TEXT PRIMARY KEY,
  org_id TEXT NOT NULL,
  username TEXT NOT NULL UNIQUE,
  email TEXT NOT NULL UNIQUE,
  role TEXT NOT NULL DEFAULT 'viewer' CHECK(role IN ('viewer', 'operator', 'commander', 'admin')),
  permissions TEXT DEFAULT '',
  webauthn_id BLOB,
  certificate_fingerprint TEXT,
  api_key_hash TEXT,
  metadata TEXT DEFAULT '{}',
  last_login TEXT,
  needs_passkey_setup INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at TEXT NOT NULL DEFAULT (datetime('now')),

  FOREIGN KEY (org_id) REFERENCES organizations(org_id) ON DELETE CASCADE
);

-- Telemetry time-series data (for historical analysis)
CREATE TABLE telemetry (
  telemetry_id TEXT PRIMARY KEY,
  entity_id TEXT NOT NULL,
  org_id TEXT NOT NULL,

  latitude REAL,
  longitude REAL,
  altitude REAL,
  heading REAL,
  velocity REAL,

  battery_level REAL,
  signal_strength REAL,
  telemetry_data TEXT DEFAULT '{}',

  timestamp TEXT NOT NULL DEFAULT (datetime('now')),

  FOREIGN KEY (entity_id) REFERENCES entities(entity_id) ON DELETE CASCADE,
  FOREIGN KEY (org_id) REFERENCES organizations(org_id) ON DELETE CASCADE
);

-- Audit log (compliance and security)
CREATE TABLE audit_log (
  audit_id TEXT PRIMARY KEY,
  org_id TEXT,
  user_id TEXT,
  action TEXT NOT NULL,
  resource_type TEXT,
  resource_id TEXT,
  changes TEXT,
  ip_address TEXT,
  user_agent TEXT,
  timestamp TEXT NOT NULL DEFAULT (datetime('now')),

  FOREIGN KEY (org_id) REFERENCES organizations(org_id) ON DELETE SET NULL,
  FOREIGN KEY (user_id) REFERENCES users(user_id) ON DELETE SET NULL
);

-- ============================================================================
-- WEBAUTHN / PASSKEY TABLES
-- ============================================================================

-- WebAuthn credentials (passkeys registered by users, stored as JSON blobs)
CREATE TABLE webauthn_credentials (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id TEXT NOT NULL,
  credential_id TEXT NOT NULL,
  credential_data TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  FOREIGN KEY (user_id) REFERENCES users(user_id) ON DELETE CASCADE
);

-- WebAuthn ceremony session data (short-lived, auto-cleaned)
CREATE TABLE webauthn_sessions (
  ceremony TEXT NOT NULL,
  session_key TEXT NOT NULL,
  session_data TEXT NOT NULL,
  user_ref TEXT DEFAULT '',
  expires_at TEXT NOT NULL,
  PRIMARY KEY (ceremony, session_key)
);

-- ============================================================================
-- API KEYS (unified provisioning for REST API + NATS)
-- ============================================================================

CREATE TABLE api_keys (
  key_id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  org_id TEXT NOT NULL,
  key_hash TEXT NOT NULL UNIQUE,
  key_prefix TEXT NOT NULL,
  name TEXT NOT NULL DEFAULT '',
  scopes TEXT NOT NULL DEFAULT '',
  role TEXT NOT NULL DEFAULT 'operator',
  nats_pub_key TEXT,
  expires_at TEXT,
  revoked INTEGER NOT NULL DEFAULT 0,
  last_used_at TEXT,
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  FOREIGN KEY (user_id) REFERENCES users(user_id) ON DELETE CASCADE,
  FOREIGN KEY (org_id) REFERENCES organizations(org_id) ON DELETE CASCADE
);

-- ============================================================================
-- USER INVITATIONS
-- ============================================================================

CREATE TABLE invites (
  invite_id TEXT PRIMARY KEY,
  org_id TEXT NOT NULL,
  email TEXT NOT NULL,
  role TEXT NOT NULL DEFAULT 'viewer' CHECK(role IN ('viewer', 'operator', 'commander', 'admin')),
  token_hash TEXT NOT NULL UNIQUE,
  invited_by_user_id TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending', 'accepted', 'expired', 'revoked')),
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at TEXT NOT NULL DEFAULT (datetime('now')),
  FOREIGN KEY (org_id) REFERENCES organizations(org_id) ON DELETE CASCADE,
  FOREIGN KEY (invited_by_user_id) REFERENCES users(user_id) ON DELETE CASCADE
);

-- ============================================================================
-- SESSIONS (persistent session store)
-- ============================================================================

CREATE TABLE IF NOT EXISTS sessions (
  session_token TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  role TEXT NOT NULL DEFAULT 'viewer',
  org_id TEXT NOT NULL DEFAULT '',
  needs_passkey_setup INTEGER NOT NULL DEFAULT 0,
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
  FOREIGN KEY (user_id) REFERENCES users(user_id)
);

-- ============================================================================
-- INDEXES
-- ============================================================================

-- Organizations
CREATE INDEX idx_organizations_name ON organizations(name);
CREATE INDEX idx_organizations_type ON organizations(org_type);

-- Entities
CREATE INDEX idx_entities_org_id ON entities(org_id);
CREATE INDEX idx_entities_type ON entities(entity_type);
CREATE INDEX idx_entities_status ON entities(status);
CREATE INDEX idx_entities_priority ON entities(priority);
CREATE INDEX idx_entities_is_live ON entities(is_live);
CREATE INDEX idx_entities_expiry ON entities(expiry_time) WHERE expiry_time IS NOT NULL;
CREATE INDEX idx_entities_updated ON entities(updated_at DESC);
CREATE INDEX idx_entities_org_type_status ON entities(org_id, entity_type, status);
CREATE INDEX idx_entities_location ON entities(latitude, longitude) WHERE latitude IS NOT NULL AND longitude IS NOT NULL;

-- Entity Relationships
CREATE INDEX idx_relationships_source ON entity_relationships(source_id);
CREATE INDEX idx_relationships_target ON entity_relationships(target_id);
CREATE INDEX idx_relationships_type ON entity_relationships(relationship_type);

-- Messages
CREATE INDEX idx_messages_org_id ON messages(org_id);
CREATE INDEX idx_messages_type ON messages(message_type);
CREATE INDEX idx_messages_topic ON messages(topic);
CREATE INDEX idx_messages_created ON messages(created_at DESC);
CREATE INDEX idx_messages_expires ON messages(expires_at) WHERE expires_at IS NOT NULL;

-- Missions
CREATE INDEX idx_missions_org_id ON missions(org_id);
CREATE INDEX idx_missions_status ON missions(status);
CREATE INDEX idx_missions_created ON missions(created_at DESC);

-- Users
CREATE INDEX idx_users_org_id ON users(org_id);
CREATE INDEX idx_users_username ON users(username);
CREATE INDEX idx_users_email ON users(email);
CREATE INDEX idx_users_role ON users(role);

-- Telemetry
CREATE INDEX idx_telemetry_entity_id ON telemetry(entity_id);
CREATE INDEX idx_telemetry_timestamp ON telemetry(timestamp DESC);
CREATE INDEX idx_telemetry_entity_time ON telemetry(entity_id, timestamp DESC);

-- Audit Log
CREATE INDEX idx_audit_org_id ON audit_log(org_id);
CREATE INDEX idx_audit_user_id ON audit_log(user_id);
CREATE INDEX idx_audit_action ON audit_log(action);
CREATE INDEX idx_audit_timestamp ON audit_log(timestamp DESC);

-- WebAuthn Credentials
CREATE INDEX idx_webauthn_creds_user ON webauthn_credentials(user_id);
CREATE UNIQUE INDEX idx_webauthn_creds_credential_id_unique ON webauthn_credentials(credential_id);

-- WebAuthn Sessions (for cleanup)
CREATE INDEX idx_webauthn_sessions_expires ON webauthn_sessions(expires_at);
CREATE INDEX idx_webauthn_sessions_ceremony ON webauthn_sessions(ceremony);

-- API Keys
CREATE INDEX idx_api_keys_hash ON api_keys(key_hash);
CREATE INDEX idx_api_keys_user ON api_keys(user_id);
CREATE INDEX idx_api_keys_org ON api_keys(org_id);
CREATE INDEX idx_api_keys_nats_pub ON api_keys(nats_pub_key) WHERE nats_pub_key IS NOT NULL;

-- Sessions
CREATE INDEX idx_sessions_expires ON sessions(expires_at);
CREATE INDEX idx_sessions_user ON sessions(user_id);

-- Invites
CREATE INDEX idx_invites_token ON invites(token_hash);
CREATE INDEX idx_invites_email ON invites(email);
CREATE INDEX idx_invites_org ON invites(org_id);
CREATE INDEX idx_invites_status ON invites(status);

-- ============================================================================
-- TRIGGERS
-- ============================================================================

-- Update timestamp triggers
CREATE TRIGGER update_organizations_timestamp
  AFTER UPDATE ON organizations
  FOR EACH ROW
  BEGIN
    UPDATE organizations SET updated_at = datetime('now') WHERE org_id = NEW.org_id;
  END;

CREATE TRIGGER update_entities_timestamp
  AFTER UPDATE ON entities
  FOR EACH ROW
  BEGIN
    UPDATE entities SET updated_at = datetime('now') WHERE entity_id = NEW.entity_id;
  END;

CREATE TRIGGER update_relationships_timestamp
  AFTER UPDATE ON entity_relationships
  FOR EACH ROW
  BEGIN
    UPDATE entity_relationships SET updated_at = datetime('now') WHERE relationship_id = NEW.relationship_id;
  END;

CREATE TRIGGER update_missions_timestamp
  AFTER UPDATE ON missions
  FOR EACH ROW
  BEGIN
    UPDATE missions SET updated_at = datetime('now') WHERE mission_id = NEW.mission_id;
  END;

CREATE TRIGGER update_users_timestamp
  AFTER UPDATE ON users
  FOR EACH ROW
  BEGIN
    UPDATE users SET updated_at = datetime('now') WHERE user_id = NEW.user_id;
  END;

-- ============================================================================
-- VIEWS
-- ============================================================================

-- Active entities view
CREATE VIEW active_entities AS
SELECT
  e.*,
  o.name as org_name,
  o.org_type
FROM entities e
JOIN organizations o ON e.org_id = o.org_id
WHERE
  e.status = 'active'
  AND (e.expiry_time IS NULL OR e.expiry_time > datetime('now'));

-- Latest telemetry view
CREATE VIEW latest_telemetry AS
SELECT *
FROM telemetry t1
WHERE timestamp = (
  SELECT MAX(timestamp)
  FROM telemetry t2
  WHERE t2.entity_id = t1.entity_id
);

-- Entity relationships graph view
CREATE VIEW entity_graph AS
SELECT
  er.relationship_id,
  er.relationship_type,
  s.entity_id as source_id,
  s.entity_type as source_type,
  s.status as source_status,
  t.entity_id as target_id,
  t.entity_type as target_type,
  t.status as target_status,
  er.metadata,
  er.created_at
FROM entity_relationships er
JOIN entities s ON er.source_id = s.entity_id
JOIN entities t ON er.target_id = t.entity_id;
