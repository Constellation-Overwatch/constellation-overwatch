package db

import (
	"database/sql"
	"embed"
	"fmt"
	"log"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

//go:embed schema.sql
var schemaFS embed.FS

// Service represents the database service with connection management
type Service struct {
	DB     *sql.DB
	DBPath string
}

// Config holds database configuration
type Config struct {
	DBPath         string
	MaxOpenConns   int
	MaxIdleConns   int
	AutoInitialize bool // Automatically initialize schema if DB doesn't exist
}

// DefaultConfig returns default database configuration
func DefaultConfig() *Config {
	return &Config{
		DBPath:         "./db/constellation.db",
		MaxOpenConns:   1, // SQLite doesn't handle concurrent writes well
		MaxIdleConns:   1,
		AutoInitialize: true,
	}
}

// New creates a new database service instance
func New(config *Config) (*Service, error) {
	if config == nil {
		config = DefaultConfig()
	}

	service := &Service{
		DBPath: config.DBPath,
	}

	// Check if database file exists
	dbExists := fileExists(config.DBPath)

	// Ensure the directory exists
	dbDir := filepath.Dir(config.DBPath)
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	// Open database connection
	db, err := sql.Open("sqlite3", config.DBPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Set connection pool settings
	db.SetMaxOpenConns(config.MaxOpenConns)
	db.SetMaxIdleConns(config.MaxIdleConns)
	db.SetConnMaxLifetime(0)

	service.DB = db

	// Test the connection
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// Initialize schema if database is new and auto-initialization is enabled
	if !dbExists && config.AutoInitialize {
		log.Println("Database not found, initializing schema...")
		if err := service.InitializeSchema(); err != nil {
			return nil, fmt.Errorf("failed to initialize schema: %w", err)
		}
		log.Println("Database schema initialized successfully")
	}

	log.Printf("Database service initialized: %s", config.DBPath)
	return service, nil
}

// InitializeSchema loads and executes the schema.sql file
func (s *Service) InitializeSchema() error {
	// Read schema from embedded filesystem
	schemaSQL, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		return fmt.Errorf("failed to read schema file: %w", err)
	}

	// Execute schema
	if _, err := s.DB.Exec(string(schemaSQL)); err != nil {
		return fmt.Errorf("failed to execute schema: %w", err)
	}

	return nil
}

// VerifySchema checks if the database schema is properly initialized
func (s *Service) VerifySchema() error {
	// Check if core tables exist
	requiredTables := []string{
		"organizations",
		"entities",
		"entity_relationships",
		"messages",
		"missions",
		"users",
		"telemetry",
		"audit_log",
	}

	for _, table := range requiredTables {
		var exists int
		query := `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`
		if err := s.DB.QueryRow(query, table).Scan(&exists); err != nil {
			return fmt.Errorf("failed to check table %s: %w", table, err)
		}
		if exists == 0 {
			return fmt.Errorf("required table missing: %s", table)
		}
	}

	log.Println("Schema verification successful - all required tables present")
	return nil
}

// Close closes the database connection
func (s *Service) Close() error {
	if s.DB != nil {
		log.Println("Closing database connection...")
		return s.DB.Close()
	}
	return nil
}

// GetDB returns the underlying database connection
func (s *Service) GetDB() *sql.DB {
	return s.DB
}

// Transaction executes a function within a database transaction
func (s *Service) Transaction(fn func(*sql.Tx) error) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p) // re-throw panic after rollback
		}
	}()

	if err := fn(tx); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return fmt.Errorf("transaction error: %v, rollback error: %v", err, rbErr)
		}
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// Health checks the database connection health
func (s *Service) Health() error {
	if s.DB == nil {
		return fmt.Errorf("database connection is nil")
	}
	return s.DB.Ping()
}

// GetStats returns database connection statistics
func (s *Service) GetStats() sql.DBStats {
	return s.DB.Stats()
}

// fileExists checks if a file exists
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return !os.IsNotExist(err)
}

// MigrateSchema applies any pending schema migrations
// This is a placeholder for future migration support
func (s *Service) MigrateSchema() error {
	// TODO: Implement migration system
	log.Println("Schema migration not yet implemented")
	return nil
}

// GetSchemaVersion returns the current schema version
// This is a placeholder for future versioning support
func (s *Service) GetSchemaVersion() (string, error) {
	// TODO: Implement schema versioning
	return "1.0.0", nil
}
