package api

import (
	"database/sql"

	"sdr-api/internal/api/handlers"
	"sdr-api/internal/api/middleware"
	"sdr-api/pkg/config"
	"sdr-api/pkg/logger"

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

	// Type 1 client routes (SDR devices)
	type1 := api.Group("/type1")
	type1.Use(middleware.RequireAuth(cfg))
	type1.Use(middleware.RequireClientType(1))
	{
		type1.POST("/register", type1Handler.Register)
		type1.GET("/status", type1Handler.GetStatus)
		type1.PUT("/update", type1Handler.Update)
	}

	// Type 2 client routes (Data consumers)
	type2 := api.Group("/data")
	type2.Use(middleware.RequireAuth(cfg))
	type2.Use(middleware.RequireClientType(2))
	{
		type2.GET("/spectrum", type2Handler.GetSpectrum)
		type2.GET("/signal", type2Handler.GetSignal)
		type2.GET("/availability", type2Handler.GetAvailability)
	}

	// WebSocket endpoint for Type 1 clients
	router.GET("/ws", middleware.RequireAuth(cfg), middleware.RequireClientType(1), type1Handler.WebSocketHandler)

	return router
}