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
	"database/sql"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
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

// loadRuntimeEnv preserves .env convenience for local development while
// preventing a production process from inheriting checkout-local settings.
// Production configuration must come from the service supervisor.
func loadRuntimeEnv(flagPath string) (string, error) {
	envPath := findEnvFile(flagPath)
	if envPath == "" {
		return "", nil
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("OVERWATCH_ENV")), runtimeconfig.ModeProduction) {
		return "", fmt.Errorf("production forbids application .env files; configure the service supervisor and remove %s", envPath)
	}
	if err := godotenv.Load(envPath); err != nil {
		return "", fmt.Errorf("load %s: %w", envPath, err)
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("OVERWATCH_ENV")), runtimeconfig.ModeProduction) {
		return "", fmt.Errorf("production mode must not be enabled by application .env file %s", envPath)
	}
	return envPath, nil
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
		host     = startFlags.String("host", "", "Bind address (default: 127.0.0.1)")
		natsPort = startFlags.String("nats-port", "", "NATS server port (default: 4222)")
		dataDir  = startFlags.String("data-dir", "", "Data directory (default: ./data)")
		envFile  = startFlags.String("env", ".env", "Path to .env file")
	)
	startFlags.Parse(args)

	// Load .env file (flags override env vars)
	if envPath, err := loadRuntimeEnv(*envFile); err != nil {
		logger.Fatalw("Refusing to load configuration", "error", err)
	} else if envPath != "" {
		logger.Infow("Loaded development config", "path", envPath)
	} else {
		logger.Info("No .env found, using environment variables and flags")
	}

	// Apply CLI flag overrides to environment (flags take precedence)
	applyFlagOverrides(*port, *host, *natsPort, *dataDir)

	runtimeCfg, err := loadRuntimeConfig()
	if err != nil {
		logger.Fatalw("Refusing to start with invalid configuration", "error", err)
	}
	if err := prepareRuntimePaths(runtimeCfg); err != nil {
		logger.Fatalw("Refusing to start because runtime paths are not ready", "error", err)
	}
	if runtimeCfg.Production() {
		logger.Infow("Validated production deployment profile", "base_url", runtimeCfg.BaseURL, "host", runtimeCfg.Host, "port", runtimeCfg.Port)
	} else {
		logger.Warn("Running in development mode; do not expose this profile to a network")
	}

	// Initialize logger (handled by init() in logger package)
	defer logger.Sync()

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
	dbService, err := db.NewService()
	if err != nil {
		logger.Fatalw("Failed to initialize database service", "error", err)
	}
	if err := dbService.Start(ctx); err != nil {
		logger.Fatalw("Failed to start database service", "error", err)
	}

	// 2. Initialize Embedded NATS
	logger.Info("Initializing NATS service...")
	natsService, err := embeddednats.NewService()
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
	if err := bootstrapAdmin(dbService, runtimeCfg); err != nil {
		logger.Fatalw("Failed to prepare bootstrap administrator", "error", err)
	}

	// 5. Initialize API Router
	logger.Info("Initializing API router...")
	apiHandler := api.NewRouterWithOrigins(dbService.GetDB(), natsService, runtimeCfg.AllowedOrigins)

	// 6. Initialize Web Server
	logger.Info("Initializing web server...")
	webServer, err := web.NewWebService(dbService, nc, natsService, apiHandler, runtimeCfg)
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
  --host <HOST>        Network bind address (default: 127.0.0.1)
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
  PORT, HOST, NATS_PORT, OVERWATCH_DATA_DIR

  Priority: CLI flags > environment variables > .env file > defaults

Endpoints:
  Web UI      http://localhost:8080
  REST API    http://localhost:8080/api/v1/
  NATS TCP    nats://localhost:4222
  Health      http://localhost:8080/health`)
}

// bootstrapAdmin ensures an incomplete first-run administrator always has one
// current invitation. Production setup material is written only to an
// explicitly configured, create-once file and never to normal logs.
func bootstrapAdmin(dbService *db.Service, cfg runtimeconfig.Runtime) error {
	database := dbService.GetDB()

	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return fmt.Errorf("check user count: %w", err)
	}

	adminEmail := cfg.AdminEmail
	adminUserID := ""
	if count == 0 {
		logger.Info("No users found, bootstrapping default organization and admin user...")
		if _, err := database.Exec(
			`INSERT OR IGNORE INTO organizations (org_id, name, org_type, description) VALUES (?, ?, ?, ?)`,
			"default", "Default Organization", "commercial", "Auto-created default organization",
		); err != nil {
			return fmt.Errorf("create default organization: %w", err)
		}

		userSvc := services.NewUserService(database)
		admin := &services.User{
			OrgID:             "default",
			Username:          adminEmail,
			Email:             adminEmail,
			Role:              "admin",
			NeedsPasskeySetup: true,
		}
		if err := userSvc.CreateUser(admin); err != nil {
			return fmt.Errorf("create bootstrap admin: %w", err)
		}
		adminUserID = admin.UserID
	} else {
		err := database.QueryRow(
			`SELECT user_id, email FROM users WHERE role = 'admin' AND needs_passkey_setup = 1 ORDER BY created_at LIMIT 1`,
		).Scan(&adminUserID, &adminEmail)
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return fmt.Errorf("find incomplete bootstrap admin: %w", err)
		}
	}
	if cfg.Production() {
		exists, err := secureBootstrapFileExists(cfg.BootstrapFile)
		if err != nil {
			return err
		}
		if exists {
			logger.Infow("Incomplete bootstrap admin still has secure setup material", "email", adminEmail, "path", cfg.BootstrapFile)
			return nil
		}
	}

	if _, err := database.Exec(
		`UPDATE invites SET status = 'revoked', updated_at = ? WHERE invited_by_user_id = ? AND status = 'pending'`,
		time.Now().Format(time.RFC3339), adminUserID,
	); err != nil {
		return fmt.Errorf("revoke stale bootstrap invites: %w", err)
	}

	inviteSvc := services.NewInviteService(database)
	_, plainToken, err := inviteSvc.CreateInvite("default", adminEmail, "admin", adminUserID)
	if err != nil {
		return fmt.Errorf("create bootstrap invite: %w", err)
	}
	setupURL := strings.TrimRight(cfg.BaseURL, "/") + "/invite/" + plainToken

	if cfg.Production() {
		if err := writeBootstrapFile(cfg.BootstrapFile, adminEmail, setupURL); err != nil {
			return err
		}
		logger.Infow("Bootstrap admin prepared; setup material written to secure file", "email", adminEmail, "path", cfg.BootstrapFile)
		return nil
	}

	logger.Infow("Development bootstrap admin created", "email", adminEmail, "user_id", adminUserID)
	fmt.Printf("\n  Admin account created for: %s\n", adminEmail)
	fmt.Printf("  Complete development setup at: %s\n\n", setupURL)
	return nil
}

func writeBootstrapFile(path, email, setupURL string) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create secure bootstrap file %s: %w", path, err)
	}
	removeOnFailure := true
	defer func() {
		_ = file.Close()
		if removeOnFailure {
			_ = os.Remove(path)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return fmt.Errorf("restrict bootstrap file permissions: %w", err)
	}
	if _, err := fmt.Fprintf(file, "admin_email=%s\nsetup_url=%s\n", email, setupURL); err != nil {
		return fmt.Errorf("write bootstrap file: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync bootstrap file: %w", err)
	}
	removeOnFailure = false
	return nil
}

func secureBootstrapFileExists(path string) (bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect bootstrap file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("bootstrap path must be a regular non-symlink file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return false, fmt.Errorf("bootstrap file permissions %o expose setup material; require 0600", info.Mode().Perm())
	}
	return true, nil
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
