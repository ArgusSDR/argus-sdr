package api

import (
	"database/sql"

	"argus-sdr/internal/api/handlers"
	"argus-sdr/internal/api/middleware"
	"argus-sdr/pkg/config"
	"argus-sdr/pkg/logger"

	"github.com/gin-gonic/gin"
)

func NewRouter(db *sql.DB, log *logger.Logger, cfg *config.Config) *gin.Engine {
	router := gin.New()

	// Middleware
	router.Use(middleware.Logger(log))
	router.Use(middleware.Recovery(log))
	router.Use(middleware.CORS())

	// Initialize handlers
	authHandler := handlers.NewAuthHandler(db, log, cfg)
	type1Handler := handlers.NewType1Handler(db, log, cfg)
	type2Handler := handlers.NewType2Handler(db, log, cfg)
	dataHandler := handlers.NewDataHandler(db, log, cfg)
	collectorHandler := handlers.NewCollectorHandler(db, log, cfg, dataHandler)
	iceHandler := handlers.NewICEHandler(db, log, cfg, type1Handler)

	// Set up handler dependencies
	dataHandler.SetCollectorHandler(collectorHandler)

	// Health check
	router.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	// API routes
	api := router.Group("/api")

	// Authentication routes
	auth := api.Group("/auth")
	{
		auth.POST("/register", authHandler.Register)
		auth.POST("/login", authHandler.Login)
		auth.POST("/logout", authHandler.Logout)
		auth.GET("/me", middleware.RequireAuth(cfg), authHandler.Me)
	}

	// ICE routes (WebRTC signaling and file transfer)
	ice := api.Group("/ice")
	ice.Use(middleware.RequireAuth(cfg))
	{
		ice.POST("/request", iceHandler.InitiateSession)
		ice.POST("/signal", iceHandler.Signal)
		ice.GET("/signals/:session_id", iceHandler.GetSignals)
		ice.GET("/sessions", iceHandler.GetActiveSessions)
	}

	// Type 1 client routes (SDR devices)
	type1 := api.Group("/type1")
	type1.Use(middleware.RequireAuth(cfg))
	type1.Use(middleware.RequireClientType(1))
	{
		type1.POST("/register", type1Handler.Register)
		type1.GET("/status", type1Handler.GetStatus)
		type1.PUT("/update", type1Handler.Update)
	}

	// Data request routes (new modes system)
	data := api.Group("/data")
	data.Use(middleware.RequireAuth(cfg))
	{
		data.POST("/request", dataHandler.RequestData)
		data.GET("/status/:id", dataHandler.GetRequestStatus)
		data.GET("/downloads/:id", dataHandler.GetAvailableDownloads)
		data.GET("/requests", dataHandler.ListRequests)
		data.GET("/download/:id", dataHandler.DownloadFile)

		// Legacy Type 2 routes
		data.GET("/spectrum", middleware.RequireClientType(2), type2Handler.GetSpectrum)
		data.GET("/signal", middleware.RequireClientType(2), type2Handler.GetSignal)
		data.GET("/availability", middleware.RequireClientType(2), type2Handler.GetAvailability)
	}

	// WebSocket endpoint for Type 1 clients (legacy)
	router.GET("/ws", middleware.RequireAuth(cfg), middleware.RequireClientType(1), type1Handler.WebSocketHandler)

	// WebSocket endpoint for collector clients (new modes system)
	router.GET("/collector-ws", collectorHandler.WebSocketHandler)

	return router
}