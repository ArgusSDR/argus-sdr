package models

import (
	"time"
)

type User struct {
	ID           int       `json:"id" db:"id"`
	Email        string    `json:"email" db:"email"`
	PasswordHash string    `json:"-" db:"password_hash"`
	ClientType   int       `json:"client_type" db:"client_type"` // 1 or 2
	CreatedAt    time.Time `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time `json:"updated_at" db:"updated_at"`
}

type Type1Client struct {
	ID           int       `json:"id" db:"id"`
	UserID       int       `json:"user_id" db:"user_id"`
	ClientName   string    `json:"client_name" db:"client_name"`
	Status       string    `json:"status" db:"status"`
	LastSeen     *time.Time `json:"last_seen" db:"last_seen"`
	Capabilities string    `json:"capabilities" db:"capabilities"` // JSON string
}

type ActiveConnection struct {
	ID           int       `json:"id" db:"id"`
	ClientID     int       `json:"client_id" db:"client_id"`
	ConnectionID string    `json:"connection_id" db:"connection_id"`
	ConnectedAt  time.Time `json:"connected_at" db:"connected_at"`
}

// Auth request/response structures
type LoginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=6"`
}

type RegisterRequest struct {
	Email      string `json:"email" binding:"required,email"`
	Password   string `json:"password" binding:"required,min=6"`
	ClientType int    `json:"client_type" binding:"required,oneof=1 2"`
}

type AuthResponse struct {
	Token string `json:"token"`
	User  User   `json:"user"`
}

type Type1RegisterRequest struct {
	ClientName   string `json:"client_name" binding:"required"`
	Capabilities string `json:"capabilities"`
}