package database

import (
	"database/sql"
	"os"

	_ "github.com/mattn/go-sqlite3"
)

func Initialize(dbPath string) (*sql.DB, error) {
	// Create directory if it doesn't exist
	if dir := dbPath[:len(dbPath)-len("/sdr.db")]; dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, err
		}
	}

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}

	// Test the connection
	if err := db.Ping(); err != nil {
		return nil, err
	}

	return db, nil
}

func Migrate(db *sql.DB) error {
	migrations := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			email TEXT UNIQUE NOT NULL,
			password_hash TEXT NOT NULL,
			client_type INTEGER NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS type1_clients (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL,
			client_name TEXT NOT NULL,
			status TEXT DEFAULT 'registered',
			last_seen DATETIME,
			capabilities TEXT,
			FOREIGN KEY (user_id) REFERENCES users(id)
		)`,
		`CREATE TABLE IF NOT EXISTS active_connections (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			client_id INTEGER NOT NULL,
			connection_id TEXT UNIQUE NOT NULL,
			connected_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (client_id) REFERENCES type1_clients(id)
		)`,
		`CREATE TABLE IF NOT EXISTS ice_sessions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT UNIQUE NOT NULL,
			initiator_user_id INTEGER NOT NULL,
			target_user_id INTEGER,
			initiator_client_type INTEGER NOT NULL,
			target_client_type INTEGER NOT NULL,
			status TEXT DEFAULT 'pending',
			offer_sdp TEXT,
			answer_sdp TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (initiator_user_id) REFERENCES users(id),
			FOREIGN KEY (target_user_id) REFERENCES users(id)
		)`,
		`CREATE TABLE IF NOT EXISTS data_requests (
			id TEXT PRIMARY KEY,
			request_type TEXT NOT NULL,
			parameters TEXT,
			requested_by INTEGER NOT NULL,
			assigned_station TEXT,
			status TEXT DEFAULT 'pending',
			file_path TEXT,
			file_size INTEGER,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			completed_at DATETIME,
			FOREIGN KEY (requested_by) REFERENCES users(id)
		)`,
		`CREATE TABLE IF NOT EXISTS collector_responses (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			request_id TEXT NOT NULL,
			station_id TEXT NOT NULL,
			status TEXT NOT NULL,
			file_path TEXT,
			download_url TEXT,
			file_size INTEGER,
			error_message TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			completed_at DATETIME,
			FOREIGN KEY (request_id) REFERENCES data_requests(id),
			UNIQUE(request_id, station_id)
		)`,
		`CREATE TABLE IF NOT EXISTS collector_sessions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			station_id TEXT UNIQUE NOT NULL,
			connected_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			last_heartbeat DATETIME DEFAULT CURRENT_TIMESTAMP,
			status TEXT DEFAULT 'connected'
		)`,
		`CREATE TABLE IF NOT EXISTS ice_candidates (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			user_id INTEGER NOT NULL,
			candidate TEXT NOT NULL,
			sdp_mline_index INTEGER NOT NULL,
			sdp_mid TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (session_id) REFERENCES ice_sessions(session_id),
			FOREIGN KEY (user_id) REFERENCES users(id)
		)`,
		`CREATE TABLE IF NOT EXISTS file_transfers (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			file_name TEXT NOT NULL,
			file_size INTEGER NOT NULL,
			file_type TEXT,
			request_type TEXT NOT NULL,
			parameters TEXT,
			status TEXT DEFAULT 'pending',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			completed_at DATETIME,
			FOREIGN KEY (session_id) REFERENCES ice_sessions(session_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_users_email ON users(email)`,
		`CREATE INDEX IF NOT EXISTS idx_type1_clients_user_id ON type1_clients(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_active_connections_client_id ON active_connections(client_id)`,
		`CREATE INDEX IF NOT EXISTS idx_ice_sessions_session_id ON ice_sessions(session_id)`,
		`CREATE INDEX IF NOT EXISTS idx_ice_candidates_session_id ON ice_candidates(session_id)`,
		`CREATE INDEX IF NOT EXISTS idx_file_transfers_session_id ON file_transfers(session_id)`,
		`CREATE INDEX IF NOT EXISTS idx_collector_responses_request_id ON collector_responses(request_id)`,
		`CREATE INDEX IF NOT EXISTS idx_collector_responses_station_id ON collector_responses(station_id)`,
	}

	for _, migration := range migrations {
		if _, err := db.Exec(migration); err != nil {
			return err
		}
	}

	return nil
}

// CleanupStaleConnections resets all stale connections from previous server runs
func CleanupStaleConnections(db *sql.DB) error {
	// Clear all active connections (they're all stale on server restart)
	if _, err := db.Exec("DELETE FROM active_connections"); err != nil {
		return err
	}

	// Reset all connected clients to disconnected
	if _, err := db.Exec("UPDATE type1_clients SET status = 'disconnected' WHERE status = 'connected'"); err != nil {
		return err
	}

	// Clear all ICE sessions and candidates (they're all stale on server restart)
	if _, err := db.Exec("DELETE FROM ice_candidates"); err != nil {
		return err
	}
	if _, err := db.Exec("DELETE FROM ice_sessions"); err != nil {
		return err
	}

	return nil
}