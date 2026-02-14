// @title Constellation Overwatch API
// @version 1.0
// @description C4ISR (Command, Control, Communications, and Intelligence) server mesh for drone/robotic communication
// @termsOfService https://github.com/Constellation-Overwatch/constellation-overwatch

// @contact.name Constellation Overwatch Support
// @contact.url https://github.com/Constellation-Overwatch/constellation-overwatch/issues
// @contact.email support@constellation-overwatch.com

// @license.name MIT
// @license.url https://github.com/Constellation-Overwatch/constellation-overwatch/blob/main/LICENSE

// @host localhost:8080
// @BasePath /api/v1
// @schemes http https

// @securityDefinitions.apikey BearerAuth
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
	"syscall"
	"time"

	"github.com/Constellation-Overwatch/constellation-overwatch/api"
	"github.com/Constellation-Overwatch/constellation-overwatch/db"
	embeddednats "github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/embedded-nats"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/logger"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/transcoder"
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
	// Override default flag usage with custom help
	flag.Usage = printHelp

	// CLI flags
	var (
		showVersion = flag.Bool("version", false, "Print version and exit")
		showHelp    = flag.Bool("help", false, "Show help message")
		doUpdate    = flag.Bool("update", false, "Update to the latest version")
		tuiMode     = flag.Bool("tui", false, "Start with TUI dashboard instead of headless mode")
		port        = flag.String("port", "", "Web UI and API port (default: 8080)")
		host        = flag.String("host", "", "Bind address (default: 0.0.0.0)")
		natsPort    = flag.String("nats-port", "", "NATS server port (default: 4222)")
		token       = flag.String("token", "", "Overwatch auth token (for API and NATS)")
		dataDir     = flag.String("data-dir", "", "Data directory (default: ./data)")
		envFile     = flag.String("env", ".env", "Path to .env file")
	)
	flag.Parse()

	if *showVersion {
		fmt.Printf("overwatch %s (commit: %s, built: %s)\n", version, commit, date)
		os.Exit(0)
	}

	if *showHelp {
		printHelp()
		os.Exit(0)
	}

	if *doUpdate {
		if err := updater.Update(version, false); err != nil {
			fmt.Fprintf(os.Stderr, "Update failed: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

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
	applyFlagOverrides(*port, *host, *natsPort, *token, *dataDir)

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

	// Wait for NATS to be ready
	time.Sleep(1 * time.Second)

	// Get NATS connection
	nc := natsService.Connection()

	// 3. Initialize Workers
	logger.Info("Initializing workers...")
	workerManager, err := workers.NewManager(natsService, dbService.GetDB())
	if err != nil {
		logger.Fatalw("Failed to initialize worker manager", "error", err)
	}
	if err := workerManager.Start(); err != nil {
		logger.Fatalw("Failed to start workers", "error", err)
	}

	// 3b. Initialize Video Transcoder (converts MPEG-TS to JPEG)
	logger.Info("Initializing video transcoder...")
	videoTranscoder := transcoder.New(nc)
	go func() {
		if err := videoTranscoder.Start(ctx); err != nil {
			logger.Errorw("Video transcoder error", "error", err)
		}
	}()

	// 4. Initialize API Router
	logger.Info("Initializing API router...")
	apiHandler := api.NewRouter(dbService.GetDB(), natsService)

	// 5. Initialize Web Server
	logger.Info("Initializing web server...")
	webServer, err := web.NewWebService(dbService, nc, natsService, apiHandler)
	if err != nil {
		logger.Fatalw("Failed to initialize web server", "error", err)
	}
	if err := webServer.Start(ctx); err != nil {
		logger.Fatalw("Failed to start web server", "error", err)
	}

	logger.Info("All services started successfully")

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

	// Stop services in reverse order with timeout context
	logger.Info("Stopping web server...")
	if err := webServer.Stop(shutdownCtx); err != nil {
		logger.Errorw("Error stopping web server", "error", err)
	}

	logger.Info("Stopping workers...")
	if err := workerManager.Stop(shutdownCtx); err != nil {
		logger.Errorw("Error stopping workers", "error", err)
	}

	logger.Info("Stopping NATS service...")
	if err := natsService.Stop(shutdownCtx); err != nil {
		logger.Errorw("Error stopping NATS service", "error", err)
	}

	logger.Info("Stopping database service...")
	if err := dbService.Stop(shutdownCtx); err != nil {
		logger.Errorw("Error stopping database service", "error", err)
	}

	// Cancel main context
	cancel()

	logger.Info("Shutdown complete")
}

func printHelp() {
	fmt.Println(`Constellation Overwatch - Edge C4ISR Server Mesh

USAGE:
    overwatch [OPTIONS]

OPTIONS:
    --tui                  Start with TUI dashboard (interactive terminal UI)
    --port <PORT>          HTTP server port for Web UI and REST API (default: 8080)
    --host <HOST>          Network bind address (default: 0.0.0.0)
    --nats-port <PORT>     NATS TCP port for edge device connections (default: 4222)
    --token <TOKEN>        Auth token for API and NATS (default: reindustrialize-dev-token)
    --data-dir <PATH>      Data directory for database and NATS storage (default: ./data)
    --env <PATH>           Path to .env configuration file (default: .env)
    --update               Download and install the latest version
    --version              Print version and exit
    --help                 Show this help message

QUICK START:
    # Run with defaults (headless)
    overwatch

    # Run with TUI dashboard
    overwatch --tui

    # Run on a different port
    overwatch --port 9090

    # Run with custom token
    overwatch --token mysecuretoken

    # Update to the latest version
    overwatch --update

TUI CONTROLS:
    Tab/Shift+Tab  Navigate between panels
    j/k or arrows  Scroll within panel
    v              Toggle entities/streams view
    r              Refresh all data
    ?              Show help
    q              Quit

ENVIRONMENT:
    All options can also be set via environment variables or .env file:
    PORT, HOST, NATS_PORT, OVERWATCH_TOKEN, NATS_DATA_DIR, DB_PATH

    Priority: CLI flags > environment variables > .env file > defaults

ENDPOINTS:
    Web UI:     http://localhost:8080
    REST API:   http://localhost:8080/api/v1/
    NATS TCP:   nats://localhost:4222 (edge devices connect here with token auth)
    Health:     http://localhost:8080/health

DOCUMENTATION:
    https://github.com/Constellation-Overwatch/constellation-overwatch`)
}

func applyFlagOverrides(port, host, natsPort, token, dataDir string) {
	if port != "" {
		os.Setenv("PORT", port)
	}
	if host != "" {
		os.Setenv("HOST", host)
	}
	if natsPort != "" {
		os.Setenv("NATS_PORT", natsPort)
	}
	if token != "" {
		os.Setenv("OVERWATCH_TOKEN", token)
	}
	if dataDir != "" {
		os.Setenv("NATS_DATA_DIR", dataDir+"/overwatch")
		os.Setenv("DB_PATH", dataDir+"/db/constellation.db")
	}
}
