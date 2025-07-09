package handlers

import (
	"database/sql"
	"net/http"
	"time"

	"argus-sdr/internal/auth"
	"argus-sdr/internal/models"
	"argus-sdr/pkg/config"
	"argus-sdr/pkg/logger"

	"github.com/gin-gonic/gin"
)

type AuthHandler struct {
	db  *sql.DB
	log *logger.Logger
	cfg *config.Config
}

func NewAuthHandler(db *sql.DB, log *logger.Logger, cfg *config.Config) *AuthHandler {
	return &AuthHandler{
		db:  db,
		log: log,
		cfg: cfg,
	}
}

func (h *AuthHandler) Register(c *gin.Context) {
	var req models.RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Check if user already exists
	var existingID int
	err := h.db.QueryRow("SELECT id FROM users WHERE email = ?", req.Email).Scan(&existingID)
	if err != sql.ErrNoRows {
		c.JSON(http.StatusConflict, gin.H{"error": "User already exists"})
		return
	}

	// Hash password
	hashedPassword, err := auth.HashPassword(req.Password, h.cfg.Auth.BCryptCost)
	if err != nil {
		h.log.Error("Failed to hash password: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
		return
	}

	// Insert user
	result, err := h.db.Exec(
		"INSERT INTO users (email, password_hash, client_type) VALUES (?, ?, ?)",
		req.Email, hashedPassword, req.ClientType,
	)
	if err != nil {
		h.log.Error("Failed to create user: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create user"})
		return
	}

	userID, _ := result.LastInsertId()

	// Generate token
	token, err := auth.GenerateToken(int(userID), req.Email, req.ClientType, h.cfg.Auth.JWTSecret, h.cfg.Auth.TokenExpiry)
	if err != nil {
		h.log.Error("Failed to generate token: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate token"})
		return
	}

	user := models.User{
		ID:         int(userID),
		Email:      req.Email,
		ClientType: req.ClientType,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	c.JSON(http.StatusCreated, models.AuthResponse{
		Token: token,
		User:  user,
	})
}

func (h *AuthHandler) Login(c *gin.Context) {
	var req models.LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Get user from database
	var user models.User
	err := h.db.QueryRow(
		"SELECT id, email, password_hash, client_type, created_at, updated_at FROM users WHERE email = ?",
		req.Email,
	).Scan(&user.ID, &user.Email, &user.PasswordHash, &user.ClientType, &user.CreatedAt, &user.UpdatedAt)

	if err == sql.ErrNoRows {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid credentials"})
		return
	}
	if err != nil {
		h.log.Error("Database error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
		return
	}

	// Check password
	if !auth.CheckPasswordHash(req.Password, user.PasswordHash) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid credentials"})
		return
	}

	// Generate token
	token, err := auth.GenerateToken(user.ID, user.Email, user.ClientType, h.cfg.Auth.JWTSecret, h.cfg.Auth.TokenExpiry)
	if err != nil {
		h.log.Error("Failed to generate token: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate token"})
		return
	}

	c.JSON(http.StatusOK, models.AuthResponse{
		Token: token,
		User:  user,
	})
}

func (h *AuthHandler) Logout(c *gin.Context) {
	// For JWT, logout is handled client-side by discarding the token
	// In a production system, you might want to implement token blacklisting
	c.JSON(http.StatusOK, gin.H{"message": "Logged out successfully"})
}

func (h *AuthHandler) Me(c *gin.Context) {
	userID, _ := c.Get("user_id")

	var user models.User
	err := h.db.QueryRow(
		"SELECT id, email, client_type, created_at, updated_at FROM users WHERE id = ?",
		userID,
	).Scan(&user.ID, &user.Email, &user.ClientType, &user.CreatedAt, &user.UpdatedAt)

	if err != nil {
		h.log.Error("Failed to get user: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get user"})
		return
	}

	c.JSON(http.StatusOK, user)
}