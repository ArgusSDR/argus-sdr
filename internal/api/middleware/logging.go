package middleware

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"time"

	"argus-sdr/pkg/logger"

	"github.com/gin-gonic/gin"
)

func Logger(log *logger.Logger) gin.HandlerFunc {
	return gin.LoggerWithFormatter(func(param gin.LogFormatterParams) string {
		// Get user info if available
		userInfo := ""
		if userID, exists := param.Keys["user_id"]; exists {
			if clientType, hasType := param.Keys["client_type"]; hasType {
				userInfo = fmt.Sprintf(" user_id=%v type=%v", userID, clientType)
			}
		}

		// Enhanced request logging
		log.Info("API Request: [%s] %s %s -> %d (%s) from %s%s",
			param.TimeStamp.Format(time.RFC3339),
			param.Method,
			param.Path,
			param.StatusCode,
			param.Latency,
			param.ClientIP,
			userInfo,
		)
		return ""
	})
}

// RequestLogger provides detailed request logging with body content for debugging
func RequestLogger(log *logger.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		raw := c.Request.URL.RawQuery

		// Log request details
		log.Debug("Request Start: %s %s from %s", c.Request.Method, path, c.ClientIP())
		
		// Log headers for authentication requests
		if strings.Contains(path, "/auth/") || strings.Contains(path, "/ice/") {
			if auth := c.GetHeader("Authorization"); auth != "" {
				log.Debug("Auth header present: %s...", auth[:min(len(auth), 20)])
			}
		}

		// Log request body for POST/PUT requests (excluding sensitive data)
		if c.Request.Method == "POST" || c.Request.Method == "PUT" {
			if shouldLogBody(path) {
				body, _ := io.ReadAll(c.Request.Body)
				c.Request.Body = io.NopCloser(bytes.NewBuffer(body))
				
				bodyStr := string(body)
				if len(bodyStr) > 500 {
					bodyStr = bodyStr[:500] + "..."
				}
				log.Debug("Request body: %s", sanitizeBody(bodyStr))
			}
		}

		c.Next()

		// Log response details
		latency := time.Since(start)
		status := c.Writer.Status()
		
		if raw != "" {
			path = path + "?" + raw
		}

		// Get user context if available
		userID, hasUser := c.Get("user_id")
		clientType, hasType := c.Get("client_type")
		
		userContext := ""
		if hasUser && hasType {
			userContext = fmt.Sprintf(" [user:%v type:%v]", userID, clientType)
		}

		log.Info("Request Complete: %s %s -> %d (%v)%s",
			c.Request.Method,
			path,
			status,
			latency,
			userContext,
		)

		// Log errors
		if status >= 400 {
			log.Warn("Request failed: %s %s -> %d from %s%s",
				c.Request.Method,
				path,
				status,
				c.ClientIP(),
				userContext,
			)
		}
	}
}

func shouldLogBody(path string) bool {
	// Don't log sensitive authentication data
	if strings.Contains(path, "/login") || strings.Contains(path, "/register") {
		return false
	}
	// Log other POST/PUT requests for debugging
	return true
}

func sanitizeBody(body string) string {
	// Remove sensitive fields from logs
	if strings.Contains(body, "password") {
		return strings.ReplaceAll(body, `"password":"[^"]*"`, `"password":"***"`)
	}
	return body
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func Recovery(log *logger.Logger) gin.HandlerFunc {
	return gin.RecoveryWithWriter(gin.DefaultWriter, func(c *gin.Context, recovered interface{}) {
		log.Error("Panic recovered: %v", recovered)
		c.AbortWithStatus(500)
	})
}

func CORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}