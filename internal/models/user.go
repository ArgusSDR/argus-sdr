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

// ICE Signaling structures
type ICECandidate struct {
	Candidate     string `json:"candidate"`
	SDPMLineIndex int    `json:"sdpMLineIndex"`
	SDPMid        string `json:"sdpMid"`
}

type SessionDescription struct {
	Type string `json:"type"`
	SDP  string `json:"sdp"`
}

type ICESignalRequest struct {
	SessionID           string              `json:"session_id" binding:"required"`
	Type                string              `json:"type" binding:"required,oneof=offer answer candidate"`
	SessionDescription  *SessionDescription `json:"session_description,omitempty"`
	ICECandidate        *ICECandidate       `json:"ice_candidate,omitempty"`
	TargetClientType    int                 `json:"target_client_type"`
	TargetClientIDs     []int               `json:"target_client_ids,omitempty"`
}

type ICESignalResponse struct {
	SessionID string `json:"session_id"`
	Success   bool   `json:"success"`
	Message   string `json:"message,omitempty"`
}

type FileTransferRequest struct {
	Parameters   string `json:"parameters"` // JSON string with request parameters (optional)
}

type FileTransferResponse struct {
	SessionID string `json:"session_id"`
	Success   bool   `json:"success"`
	Message   string `json:"message,omitempty"`
	FileURL   string `json:"file_url,omitempty"`
}