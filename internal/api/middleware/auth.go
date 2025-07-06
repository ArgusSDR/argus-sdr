package middleware

import (
	"net/http"
	"strings"

	"sdr-api/internal/auth"
	"sdr-api/pkg/config"

	"github.com/gin-gonic/gin"
)

func RequireAuth(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Authorization header required"})
			c.Abort()
			return
		}

		tokenString := strings.TrimPrefix(authHeader, "Bearer ")
		if tokenString == authHeader {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid authorization header format"})
			c.Abort()
			return
		}

		claims, err := auth.ValidateToken(tokenString, cfg.Auth.JWTSecret)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid token"})
			c.Abort()
			return
		}

		// Store user info in context
		c.Set("user_id", claims.UserID)
		c.Set("user_email", claims.Email)
		c.Set("client_type", claims.ClientType)

		c.Next()
	}
}

func RequireClientType(clientType int) gin.HandlerFunc {
	return func(c *gin.Context) {
		userClientType, exists := c.Get("client_type")
		if !exists {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Client type not found"})
			c.Abort()
			return
		}

		if userClientType != clientType {
			c.JSON(http.StatusForbidden, gin.H{"error": "Access denied for client type"})
			c.Abort()
			return
		}

		c.Next()
	}
}