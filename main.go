package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"argus-sdr/internal/api"
	"argus-sdr/internal/collector"
	"argus-sdr/internal/database"
	"argus-sdr/internal/receiver"
	"argus-sdr/pkg/config"
	"argus-sdr/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/spf13/cobra"
)

var (
	serverMode   string
	serverPort   int
	stationID    string
	apiServerURL string
	dataDir      string
	receiverID   string
	receiverAPIURL string
	downloadDir  string
)

var rootCmd = &cobra.Command{
	Use:   "argus-sdr",
	Short: "SDR API system with three operational modes",
	Long: `Argus SDR system supports three operational modes:
- api: Run the REST API server (default)
- collector: Run the SDR data collection client
- receiver: Run the data request client`,
}

var apiCmd = &cobra.Command{
	Use:   "api",
	Short: "Run the API server",
	Long:  `Run the REST API server that coordinates between collector and receiver clients.`,
	Run:   runAPIServer,
}

var collectorCmd = &cobra.Command{
	Use:   "collector",
	Short: "Run SDR collector client",
	Long:  `Run the SDR collector client that connects to the API server and processes data collection requests.`,
	Run:   runCollectorClient,
}

var receiverCmd = &cobra.Command{
	Use:   "receiver",
	Short: "Run SDR receiver client",
	Long:  `Run the SDR receiver client that requests data from the API server and downloads files from collectors.`,
	Run:   runReceiverClient,
}

func init() {
	// Add collector flags
	collectorCmd.Flags().StringVar(&stationID, "station-id", "", "Station ID (overrides STATION_ID environment variable)")
	collectorCmd.Flags().StringVar(&apiServerURL, "api-server-url", "", "API server URL (overrides API_SERVER_URL environment variable)")
	collectorCmd.Flags().StringVar(&dataDir, "data-dir", "", "Data directory (overrides DATA_DIR environment variable)")

	// Add receiver flags
	receiverCmd.Flags().StringVar(&receiverID, "receiver-id", "", "Receiver ID (overrides RECEIVER_ID environment variable)")
	receiverCmd.Flags().StringVar(&receiverAPIURL, "api-server-url", "", "API server URL (overrides API_SERVER_URL environment variable)")
	receiverCmd.Flags().StringVar(&downloadDir, "download-dir", "", "Download directory (overrides DOWNLOAD_DIR environment variable)")

	// Add subcommands
	rootCmd.AddCommand(apiCmd)
	rootCmd.AddCommand(collectorCmd)
	rootCmd.AddCommand(receiverCmd)

	// Set default command to api if no subcommand is specified
	rootCmd.CompletionOptions.DisableDefaultCmd = true
}

func runAPIServer(cmd *cobra.Command, args []string) {
	// Initialize logger
	log := logger.New()

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatal("Failed to load configuration: %v", err)
	}

	// Initialize database
	db, err := database.Initialize(cfg.Database.Path)
	if err != nil {
		log.Fatal("Failed to initialize database: %v", err)
	}
	defer db.Close()

	// Run migrations
	if err := database.Migrate(db); err != nil {
		log.Fatal("Failed to run migrations: %v", err)
	}

	// Clean up stale connections from previous runs
	if err := database.CleanupStaleConnections(db); err != nil {
		log.Fatal("Failed to cleanup stale connections: %v", err)
	}

	// Set Gin mode
	if cfg.Environment == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	// Initialize API router
	router := api.NewRouter(db, log, cfg)

	// Create HTTP server
	server := &http.Server{
		Addr:    cfg.Server.Address,
		Handler: router,
	}

	// Start server in goroutine
	go func() {
		log.Info("Starting API server on %s", cfg.Server.Address)
		if cfg.SSL.Enabled {
			// Use LetsEncrypt in production
			if err := server.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
				log.Fatal("Failed to start HTTPS server: %v", err)
			}
		} else {
			if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatal("Failed to start HTTP server: %v", err)
			}
		}
	}()

	// Wait for interrupt signal to gracefully shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info("Shutting down server...")

	// Graceful shutdown with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatal("Server forced to shutdown: %v", err)
	}

	log.Info("Server exited")
}

func runCollectorClient(cmd *cobra.Command, args []string) {
	// Initialize logger
	log := logger.New()

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatal("Failed to load configuration: %v", err)
	}

	// Override config with command line flags if provided
	if stationID != "" {
		cfg.Collector.StationID = stationID
	}
	if apiServerURL != "" {
		cfg.Collector.APIServerURL = apiServerURL
	}
	if dataDir != "" {
		cfg.Collector.DataDir = dataDir
	}

	// Validate collector configuration
	if cfg.Collector.StationID == "" {
		log.Fatal("Station ID is required. Provide via --station-id flag or STATION_ID environment variable")
	}
	if cfg.Collector.APIServerURL == "" {
		log.Fatal("API server URL is required. Provide via --api-server-url flag or API_SERVER_URL environment variable")
	}

	// Create collector instance
	client := &collector.Client{
		ID:             cfg.Collector.StationID,
		StationID:      cfg.Collector.StationID,
		APIServerURL:   cfg.Collector.APIServerURL,
		DataDir:        cfg.Collector.DataDir,
		ContainerImage: cfg.Collector.ContainerImage,
		Logger:         log,
	}

	log.Info("Starting collector client (Station: %s)", cfg.Collector.StationID)

	// Start the collector client
	if err := client.Start(); err != nil {
		log.Fatal("Failed to start collector: %v", err)
	}
}

func runReceiverClient(cmd *cobra.Command, args []string) {
	// Initialize logger
	log := logger.New()

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatal("Failed to load configuration: %v", err)
	}

	// Override config with command line flags if provided
	if receiverID != "" {
		cfg.Receiver.ReceiverID = receiverID
	}
	if receiverAPIURL != "" {
		cfg.Receiver.APIServerURL = receiverAPIURL
	}
	if downloadDir != "" {
		cfg.Receiver.DownloadDir = downloadDir
	}

	// Validate receiver configuration
	if cfg.Receiver.ReceiverID == "" {
		log.Fatal("Receiver ID is required. Provide via --receiver-id flag or RECEIVER_ID environment variable")
	}
	if cfg.Receiver.APIServerURL == "" {
		log.Fatal("API server URL is required. Provide via --api-server-url flag or API_SERVER_URL environment variable")
	}

	// Create receiver instance
	client := &receiver.Client{
		ID:           cfg.Receiver.ReceiverID,
		APIServerURL: cfg.Receiver.APIServerURL,
		DownloadDir:  cfg.Receiver.DownloadDir,
		Logger:       log,
	}

	log.Info("Starting receiver client (ID: %s)", cfg.Receiver.ReceiverID)

	// Start the receiver client
	if err := client.RequestAndDownload(); err != nil {
		log.Fatal("Failed to start receiver: %v", err)
	}
}

func main() {
	// If no arguments provided, default to api mode
	if len(os.Args) == 1 {
		os.Args = append(os.Args, "api")
	}

	if err := rootCmd.Execute(); err != nil {
		log.Printf("Error: %v", err)
		os.Exit(1)
	}
}