// @title Constellation Overwatch API
// @version 1.0
// @description C4ISR (Command, Control, Communications, and Intelligence) server mesh for drone/robotic communication
// @termsOfService https://github.com/Constellation-Overwatch/constellation-overwatch

// @contact.name Constellation Overwatch Support
// @contact.url https://github.com/Constellation-Overwatch/constellation-overwatch/issues
// @contact.email support@constellation-overwatch.com

// @license.name Constellation Overwatch Community Copyleft License 1.0
// @license.url https://github.com/Constellation-Overwatch/constellation-overwatch/blob/main/LICENSE

// @host localhost:8080
// @BasePath /api/v1
// @schemes http https

// @securityDefinitions.apikey APIKeyAuth
// @in header
// @name Authorization
// @description Enter the token with the `Bearer: ` prefix, e.g. "Bearer abcde12345"

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/Constellation-Overwatch/constellation-overwatch/api"
	"github.com/Constellation-Overwatch/constellation-overwatch/api/services"
	"github.com/Constellation-Overwatch/constellation-overwatch/db"
	runtimeconfig "github.com/Constellation-Overwatch/constellation-overwatch/pkg/config"
	svcmgr "github.com/Constellation-Overwatch/constellation-overwatch/pkg/services"
	embeddednats "github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/embedded-nats"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/logger"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/web"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/workers"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/tui"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/updater"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func findEnvFile(flagPath string) string {
	// 1. Explicit -env flag wins (unless it's the default)
	if flagPath != "" && flagPath != ".env" {
		return flagPath
	}

	// 2. Current directory .env (dev workflow)
	if _, err := os.Stat(".env"); err == nil {
		return ".env"
	}

	// 3. OVERWATCH_HOME/.env (binary install)
	if home := os.Getenv("OVERWATCH_HOME"); home != "" {
		if path := filepath.Join(home, ".env"); fileExists(path) {
			return path
		}
	}

	// 4. Default ~/.overwatch/.env
	if userHome, err := os.UserHomeDir(); err == nil {
		if path := filepath.Join(userHome, ".overwatch", ".env"); fileExists(path) {
			return path
		}
	}

	return "" // No config found, rely on env vars + flags
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func main() {
	// No subcommand or help flags → show splash + help
	if len(os.Args) < 2 {
		printHelp()
		return
	}

	switch os.Args[1] {
	case "start":
		cmdStart(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Printf("overwatch %s (commit: %s, built: %s)\n", version, commit, date)
	case "update":
		if err := updater.Update(version, false); err != nil {
			fmt.Fprintf(os.Stderr, "Update failed: %v\n", err)
			os.Exit(1)
		}
	case "help", "--help", "-h":
		printHelp()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", os.Args[1])
		printHelp()
		os.Exit(1)
	}
}

func cmdStart(args []string) {
	startFlags := flag.NewFlagSet("start", flag.ExitOnError)
	startFlags.Usage = printStartHelp

	var (
		tuiMode  = startFlags.Bool("tui", false, "Start with TUI dashboard instead of headless mode")
		port     = startFlags.String("port", "", "Web UI and API port (default: 8080)")
		host     = startFlags.String("host", "", "Bind address (default: 0.0.0.0)")
		natsPort = startFlags.String("nats-port", "", "NATS server port (default: 4222)")
		dataDir  = startFlags.String("data-dir", "", "Data directory (default: ./data)")
		envFile  = startFlags.String("env", ".env", "Path to .env file")
	)
	startFlags.Parse(args)

	// Load .env file (flags override env vars)
	if envPath := findEnvFile(*envFile); envPath != "" {
		if err := godotenv.Load(envPath); err != nil {
			logger.Warnw("Failed to load config", "path", envPath, "error", err)
		} else {
			logger.Infow("Loaded config", "path", envPath)
		}
	} else {
		logger.Info("No .env found, using environment variables and flags")
	}

	// Apply CLI flag overrides to environment (flags take precedence)
	applyFlagOverrides(*port, *host, *natsPort, *dataDir)

	// Initialize logger (handled by init() in logger package)
	defer logger.Sync()

	runtimeConfig, err := runtimeconfig.LoadRuntime()
	if err != nil {
		logger.Fatalw("Invalid runtime configuration; refusing startup before service initialization", "error", err)
	}
	if err := ensureRuntimeDirectories(runtimeConfig); err != nil {
		logger.Fatalw("Failed to prepare runtime directories", "error", err)
	}
	if runtimeConfig.IsProduction() {
		logger.Infow("Production configuration validated",
			"bind", runtimeConfig.Host+":"+runtimeConfig.Port,
			"base_url", runtimeConfig.BaseURL,
			"rp_id", runtimeConfig.RPID,
			"data_dir", runtimeConfig.DataDir,
			"backup_dir", runtimeConfig.BackupDir,
		)
	} else {
		logger.Warn("Running in DEVELOPMENT mode; fail-closed production requirements are not active")
	}

	// Variables for TUI mode
	var tuiProgram *tea.Program
	var tuiErrCh <-chan error
	var logHook *logger.TUIHook

	// TUI mode: Start TUI early so boot logs are visible
	if *tuiMode {
		// Create TUI log hook BEFORE any service initialization
		logHook = logger.NewTUIHook(1000)
		if err := logger.AttachTUIHook(logHook); err != nil {
			// Fall back to headless if TUI hook fails
			fmt.Fprintf(os.Stderr, "Failed to attach TUI log hook: %v\n", err)
			*tuiMode = false
		} else {
			// Start TUI immediately with minimal data sources
			tuiProgram, tuiErrCh = tui.RunMinimal(tui.MinimalDataSources{
				LogHook: logHook,
			})
		}
	}

	// Print splash screen in headless mode (TUI takes over the terminal)
	if !*tuiMode {
		printSplash()
		fmt.Println()
	}

	// Print startup banner with version info
	logger.PrintStartupBanner(version, commit, date)

	// Context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. Initialize Database
	logger.Info("Initializing database service...")
	dbService, err := db.New(&db.Config{
		DBPath:         filepath.Join(runtimeConfig.DataDir, "db", "constellation.db"),
		MaxOpenConns:   1,
		MaxIdleConns:   1,
		AutoInitialize: true,
	})
	if err != nil {
		logger.Fatalw("Failed to initialize database service", "error", err)
	}
	if err := dbService.Start(ctx); err != nil {
		logger.Fatalw("Failed to start database service", "error", err)
	}

	// 2. Initialize Embedded NATS
	logger.Info("Initializing NATS service...")
	natsConfig := embeddednats.DefaultConfig()
	natsConfig.DataDir = filepath.Join(runtimeConfig.DataDir, "overwatch")
	natsService, err := embeddednats.New(natsConfig)
	if err != nil {
		logger.Fatalw("Failed to initialize NATS service", "error", err)
	}
	if err := natsService.Start(ctx); err != nil {
		logger.Fatalw("Failed to start NATS service", "error", err)
	}

	// Get NATS connection
	nc := natsService.Connection()

	// 3. Initialize Workers
	logger.Info("Initializing workers...")
	workerManager, err := workers.NewManager(natsService, dbService.GetDB())
	if err != nil {
		logger.Fatalw("Failed to initialize worker manager", "error", err)
	}
	if err := workerManager.Start(ctx); err != nil {
		logger.Fatalw("Failed to start workers", "error", err)
	}

	// 4. Bootstrap admin user if none exist
	if err := bootstrapAdmin(dbService, runtimeConfig); err != nil {
		logger.Fatalw("Secure admin bootstrap failed", "error", err)
	}

	// 5. Initialize API Router
	logger.Info("Initializing API router...")
	apiHandler := api.NewRouterWithConfig(dbService.GetDB(), natsService, runtimeConfig)

	// 6. Initialize Web Server
	logger.Info("Initializing web server...")
	webServer, err := web.NewWebServiceWithConfig(dbService, nc, natsService, apiHandler, runtimeConfig)
	if err != nil {
		logger.Fatalw("Failed to initialize web server", "error", err)
	}
	if err := webServer.Start(ctx); err != nil {
		logger.Fatalw("Failed to start web server", "error", err)
	}

	logger.Info("All services started successfully")

	// Register services for managed shutdown (reverse order of addition)
	mgr := svcmgr.NewManager()
	mgr.AddService(dbService)
	mgr.AddService(natsService)
	mgr.AddService(workerManager)
	mgr.AddService(webServer)

	// TUI mode or headless mode
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigChan)

	if *tuiMode && tuiProgram != nil {
		// Send DataSourcesReadyMsg to TUI now that services are initialized
		tuiProgram.Send(tui.DataSourcesReadyMsg{
			WorkerManager: workerManager,
			JetStream:     workerManager.GetJetStream(),
			KeyValue:      workerManager.GetKeyValue(),
		})

		// Wait for TUI to exit or a shutdown signal
		select {
		case err := <-tuiErrCh:
			if err != nil {
				logger.Errorw("TUI error", "error", err)
			}
		case sig := <-sigChan:
			logger.Infow("Received signal, shutting down...", "signal", sig)
			tuiProgram.Quit()
			if err := <-tuiErrCh; err != nil {
				logger.Errorw("TUI error", "error", err)
			}
		}

		// Detach TUI hook before shutdown
		logger.DetachTUIHook()
		logger.Info("TUI closed, shutting down...")
	} else {
		// Headless mode: wait for interrupt signal
		sig := <-sigChan
		logger.Infow("Received signal, shutting down...", "signal", sig)
	}

	// Create shutdown context with timeout for graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	// Stop all services in reverse registration order
	logger.Info("Stopping all services...")
	if err := mgr.Stop(shutdownCtx); err != nil {
		logger.Errorw("Error during shutdown", "error", err)
	}

	// Cancel main context
	cancel()

	logger.Info("Shutdown complete")
}

// printSplash renders the boxed splash screen with logo, tagline, and version info.
func printSplash() {
	const boxWidth = 78 // inner width (between the vertical bars)

	h := "─"
	topBorder := "┌" + strings.Repeat(h, boxWidth) + "┐"
	midBorder := "├" + strings.Repeat(h, boxWidth) + "┤"
	botBorder := "└" + strings.Repeat(h, boxWidth) + "┘"

	padLine := func(content string) string {
		runeLen := utf8.RuneCountInString(content)
		pad := boxWidth - runeLen
		if pad < 0 {
			pad = 0
		}
		return "│" + content + strings.Repeat(" ", pad) + "│"
	}

	empty := padLine("")

	logo := []string{
		`      ██████╗██╗  ██╗`,
		`     ██╔════╝██║  ██║      C O N S T E L L A T I O N`,
		`     ██║     ███████║      O V E R W A T C H`,
		`     ██║     ╚════██║`,
		`     ╚██████╗     ██║      "Edge C4ISR at the speed of command"`,
		`      ╚═════╝     ╚═╝`,
	}

	versionStr := "  Constellation Overwatch"
	if version != "dev" {
		versionStr = fmt.Sprintf("  Constellation Overwatch v%s", version)
	}

	fmt.Println(topBorder)
	fmt.Println(empty)
	for _, line := range logo {
		fmt.Println(padLine(line))
	}
	fmt.Println(empty)
	fmt.Println(midBorder)
	fmt.Println(padLine(versionStr))
	fmt.Println(padLine("  Vendor-agnostic edge C4ISR data plane for drones, robots & sensors"))
	fmt.Println(empty)
	fmt.Println(padLine("  https://constellation-overwatch.dev"))
	fmt.Println(botBorder)
}

func printHelp() {
	printSplash()
	fmt.Println(`
Usage:
  overwatch <command> [options]

Commands:
  start          Start the server (headless or TUI)
  version        Print version and exit
  update         Download and install the latest version
  help           Show this help message

Quick Start:
  overwatch start              Start in headless mode
  overwatch start --tui        Start with TUI dashboard
  overwatch start --port 9090  Run on a different port

Run 'overwatch start --help' for server options.`)
}

func printStartHelp() {
	fmt.Println(`Start the Constellation Overwatch server.

Usage:
  overwatch start [options]

Options:
  --tui                Start with TUI dashboard (interactive terminal UI)
  --port <PORT>        HTTP server port for Web UI and REST API (default: 8080)
  --host <HOST>        Network bind address (default: 0.0.0.0)
  --nats-port <PORT>   NATS TCP port for edge device connections (default: 4222)
  --data-dir <PATH>    Data directory for database and NATS storage (default: ./data)
  --env <PATH>         Path to .env configuration file (default: .env)

TUI Controls:
  Tab/Shift+Tab   Navigate between panels
  j/k or arrows   Scroll within panel
  v               Toggle entities/streams view
  r               Refresh all data
  ?               Show help
  q               Quit

Environment:
  All options can also be set via environment variables or .env file:
  OVERWATCH_ENV, PORT, HOST, NATS_PORT, OVERWATCH_DATA_DIR

  Production mode also requires explicit HTTPS/RPID/origins, key hashing,
  backup, admin identity, trusted proxy policy, and secure bootstrap output.
  See docs/production-deployment.md.

  Priority: CLI flags > environment variables > .env file > defaults

Endpoints:
  Web UI      http://localhost:8080
  REST API    http://localhost:8080/api/v1/
  NATS TCP    nats://localhost:4222
  Health      http://localhost:8080/health`)
}

// bootstrapAdmin ensures at least one admin user exists for first-time setup.
// If no users exist, it creates the default org, a bootstrap admin, and a
// one-time invite token. Production writes setup material only to an exclusive
// mode-0600 file; development prints it to the local terminal. There is no
// zero-credential login path.
func bootstrapAdmin(dbService *db.Service, runtime *runtimeconfig.Runtime) error {
	database := dbService.GetDB()

	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return fmt.Errorf("failed to check user count for bootstrap: %w", err)
	}
	if count > 0 {
		return nil
	}

	logger.Info("No users found, bootstrapping default organization and admin user...")

	var bootstrapOutput *os.File
	removeBootstrapOutput := false
	if runtime.IsProduction() {
		parent := filepath.Dir(runtime.BootstrapFile)
		if err := os.MkdirAll(parent, 0700); err != nil {
			return fmt.Errorf("create bootstrap output directory: %w", err)
		}
		var err error
		bootstrapOutput, err = os.OpenFile(runtime.BootstrapFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return fmt.Errorf("open exclusive bootstrap output %s: %w", runtime.BootstrapFile, err)
		}
		removeBootstrapOutput = true
		defer func() {
			_ = bootstrapOutput.Close()
			if removeBootstrapOutput {
				_ = os.Remove(runtime.BootstrapFile)
			}
		}()
	}

	tx, err := database.Begin()
	if err != nil {
		return fmt.Errorf("begin bootstrap transaction: %w", err)
	}
	defer tx.Rollback()

	// Ensure the default organization exists
	_, err = tx.Exec(
		`INSERT OR IGNORE INTO organizations (org_id, name, org_type, description) VALUES (?, ?, ?, ?)`,
		"default", "Default Organization", "commercial", "Auto-created default organization",
	)
	if err != nil {
		return fmt.Errorf("create default organization: %w", err)
	}

	userSvc := services.NewUserService(tx)
	admin := &services.User{
		OrgID:             "default",
		Username:          runtime.AdminEmail, // email IS the identity
		Email:             runtime.AdminEmail,
		Role:              "admin",
		NeedsPasskeySetup: true,
	}

	if err := userSvc.CreateUser(admin); err != nil {
		return fmt.Errorf("create bootstrap admin: %w", err)
	}

	// Generate a one-time invite so the admin can set up their passkey.
	inviteSvc := services.NewInviteService(tx)
	_, plainToken, err := inviteSvc.CreateInvite("default", runtime.AdminEmail, "admin", admin.UserID)
	if err != nil {
		return fmt.Errorf("create bootstrap invite: %w", err)
	}

	setup := fmt.Sprintf(
		"Constellation Overwatch bootstrap\nAdmin: %s\nSetup URL: %s/invite/%s\n",
		admin.Email,
		strings.TrimRight(runtime.BaseURL, "/"),
		plainToken,
	)
	if runtime.IsProduction() {
		if _, err := bootstrapOutput.WriteString(setup); err != nil {
			return fmt.Errorf("write secure bootstrap output: %w", err)
		}
		if err := bootstrapOutput.Sync(); err != nil {
			return fmt.Errorf("sync secure bootstrap output: %w", err)
		}
		if err := bootstrapOutput.Close(); err != nil {
			return fmt.Errorf("close secure bootstrap output: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit bootstrap transaction: %w", err)
	}
	removeBootstrapOutput = false

	logger.Infow("Bootstrap admin created",
		"email", admin.Email, "user_id", admin.UserID)
	if runtime.IsProduction() {
		logger.Infow("Bootstrap setup material written to secure file",
			"path", runtime.BootstrapFile)
	} else {
		fmt.Printf("\n%s\n", setup)
	}
	return nil
}

func ensureRuntimeDirectories(runtime *runtimeconfig.Runtime) error {
	if err := os.MkdirAll(runtime.DataDir, 0700); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	if runtime.BackupDir != "" {
		if err := os.MkdirAll(runtime.BackupDir, 0700); err != nil {
			return fmt.Errorf("create backup directory: %w", err)
		}
	}
	return nil
}

func applyFlagOverrides(port, host, natsPort, dataDir string) {
	if port != "" {
		os.Setenv("PORT", port)
	}
	if host != "" {
		os.Setenv("HOST", host)
	}
	if natsPort != "" {
		os.Setenv("NATS_PORT", natsPort)
	}
	if dataDir != "" {
		os.Setenv("OVERWATCH_DATA_DIR", dataDir)
	}
}
