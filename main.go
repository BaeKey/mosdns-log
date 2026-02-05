package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"mosdns-log/api"
	"mosdns-log/config"
	"mosdns-log/model"
	"mosdns-log/service"
)

func main() {
	if err := run(); err != nil {
		slog.Error("Application failed", "error", err)
		os.Exit(1)
	}
}

// appLogFile holds the application log file handle for proper cleanup
var appLogFile *os.File

const DBFile = "mosdns.db"

func run() error {
	// CLI Flags
	configPath := flag.String("c", "config.yaml", "Path to configuration file")
	flag.Parse()

	// Load Config
	conf, err := config.LoadConfig(*configPath)
	if err != nil {
		// Use standard logger for config load failure as slog is not set up yet
		slog.Error("Failed to load config", "path", *configPath, "error", err)
		if conf == nil {
			return err
		}
	}

	// Setup Logger
	appLogFile = setupLogger(conf)

	slog.Info("Loaded config", "LogPath", conf.LogPath, "Port", conf.Port, "AppLogPath", conf.AppLogPath, "AppLogLevel", conf.AppLogLevel)

	// Database
	// Recreate DB logic: Check if exists, delete if so.
	dbFiles := []string{DBFile, DBFile + "-shm", DBFile + "-wal"}
	for _, f := range dbFiles {
		if _, err := os.Stat(f); err == nil {
			slog.Info("Removing existing database file for fresh start...", "file", f)
			if err := os.Remove(f); err != nil {
				return fmt.Errorf("failed to remove existing database file %s: %w", f, err)
			}
		}
	}

	// Enable WAL mode for better concurrency and set busy timeout
	// glebarez/sqlite uses _pragma parameter format
	dsn := fmt.Sprintf("%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", DBFile)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Error),
	})
	if err != nil {
		return fmt.Errorf("failed to connect database: %w", err)
	}

	db.Exec("PRAGMA synchronous = NORMAL;")
	db.Exec("PRAGMA temp_store = memory;")
	db.Exec("PRAGMA cache_size = -8000;")
	db.Exec("PRAGMA mmap_size = 134217728;")
	db.Exec("PRAGMA wal_autocheckpoint = 1000;")
	// Migrate
	if err := db.AutoMigrate(&model.QueryLog{}); err != nil {
		return fmt.Errorf("failed to migrate database: %w", err)
	}

	// Service: Collector
	// Use config log path. 
	logPath := conf.LogPath
	
	// Ensure log file exists
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		file, err := os.Create(logPath)
		if err != nil {
			return fmt.Errorf("failed to create log file: %w", err)
		}
		file.Close()
	}

	// Initialize Collector
	collector := service.NewCollector(db, logPath)
	collector.Start()

	// Service: Cleaner
	conf.LogPath = logPath 
	cleaner := service.NewCleaner(db, conf)
	cleaner.Start()

	// Web Server
	r := gin.Default()
	
	// Enable Gzip
	r.Use(gzip.Gzip(gzip.BestCompression))

	r.Use(func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Next()
	})

	h := api.NewHandler(db)
	h.RegisterRoutes(r)

	// Port from config
	port := conf.Port
	if port == "" {
		port = "8080"
	}
	// If it doesn't contain a colon, assume it's just a port number
	if !strings.Contains(port, ":") {
		port = ":" + port
	}

	// Create HTTP server for graceful shutdown support
	srv := &http.Server{
		Addr:    port,
		Handler: r,
	}

	// Channel to receive shutdown signals
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// Start server in goroutine
	go func() {
		slog.Info("Server starting", "port", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("Server failed", "error", err)
		}
	}()

	// Wait for shutdown signal
	<-quit
	slog.Info("Shutting down server...")

	// Create context with timeout for graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Shutdown HTTP server
	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("Server forced to shutdown", "error", err)
	}

	// Stop services
	slog.Info("Stopping collector...")
	collector.Stop()
	
	slog.Info("Stopping cleaner...")
	cleaner.Stop()

	// Close database connection and remove database files
	slog.Info("Closing database connection...")
	sqlDB, err := db.DB()
	if err != nil {
		slog.Error("Failed to get underlying DB connection", "error", err)
	} else {
		if err := sqlDB.Close(); err != nil {
			slog.Error("Failed to close database", "error", err)
		} else {
			slog.Info("Database connection closed")
		}
	}

	// Remove database file
	slog.Info("Removing database file...")
	if err := os.Remove(DBFile); err != nil && !os.IsNotExist(err) {
		slog.Error("Failed to remove database file", "error", err)
	} else {
		slog.Info("Database file removed")
	}

	// Close application log file if opened
	if appLogFile != nil {
		appLogFile.Close()
	}

	slog.Info("Server shutdown complete")
	return nil
}

func setupLogger(c *config.Config) *os.File {
	var level slog.Level
	switch strings.ToUpper(c.AppLogLevel) {
	case "DEBUG":
		level = slog.LevelDebug
	case "INFO":
		level = slog.LevelInfo
	case "WARN":
		level = slog.LevelWarn
	case "ERROR":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{
		Level: level,
	}

	var handler slog.Handler
	var logFile *os.File
	if c.AppLogPath != "" {
		file, err := os.OpenFile(c.AppLogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
		if err != nil {
			// Fallback to stdout if file opening fails
			slog.Error("Failed to open log file, falling back to stdout", "path", c.AppLogPath, "error", err)
			handler = slog.NewTextHandler(os.Stdout, opts)
		} else {
			logFile = file
			handler = slog.NewTextHandler(file, opts)
		}
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}

	logger := slog.New(handler)
	slog.SetDefault(logger)
	return logFile
}
