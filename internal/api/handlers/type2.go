package handlers

import (
	"database/sql"
	"math/rand"
	"net/http"

	"argus-sdr/pkg/config"
	"argus-sdr/pkg/logger"

	"github.com/gin-gonic/gin"
)

type Type2Handler struct {
	db  *sql.DB
	log *logger.Logger
	cfg *config.Config
}

func NewType2Handler(db *sql.DB, log *logger.Logger, cfg *config.Config) *Type2Handler {
	return &Type2Handler{
		db:  db,
		log: log,
		cfg: cfg,
	}
}

func (h *Type2Handler) GetAvailability(c *gin.Context) {
	// Get connected clients from the connection manager instead of database
	// This ensures we only count actually connected clients
	connectedClientIDs := connManager.GetConnectedClients()
	connectedCount := len(connectedClientIDs)
	minimumClients := 1

	available := connectedCount >= minimumClients

	c.JSON(http.StatusOK, gin.H{
		"available":         available,
		"connected_clients": connectedCount,
		"minimum_required":  minimumClients,
	})
}

func (h *Type2Handler) GetSpectrum(c *gin.Context) {
	// Check if we have enough Type 1 clients
	selectedClients, err := h.selectType1Clients()
	if err != nil {
		h.log.Error("Failed to select Type 1 clients: %v", err)
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Insufficient Type 1 clients available"})
		return
	}

	// For now, return mock data
	// In a real implementation, this would request data from the selected Type 1 clients
	// via their WebSocket connections and aggregate the results

	spectrumData := gin.H{
		"requested_from_clients": selectedClients,
		"spectrum_data": gin.H{
			"frequency_range": gin.H{
				"start": "88.0 MHz",
				"end":   "108.0 MHz",
			},
			"power_levels": []float64{-65.2, -67.1, -63.5, -70.0, -68.9}, // Mock data
			"timestamp":    "2024-01-01T12:00:00Z",
		},
		"aggregation_method": "average",
	}

	userID, _ := c.Get("user_id")
	h.log.Info("Spectrum data requested by user %v from clients %v", userID, selectedClients)

	c.JSON(http.StatusOK, spectrumData)
}

func (h *Type2Handler) GetSignal(c *gin.Context) {
	selectedClients, err := h.selectType1Clients()
	if err != nil {
		h.log.Error("Failed to select Type 1 clients: %v", err)
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Insufficient Type 1 clients available"})
		return
	}

	// Mock signal analysis data
	signalData := gin.H{
		"requested_from_clients": selectedClients,
		"signal_analysis": gin.H{
			"center_frequency": "100.1 MHz",
			"bandwidth":        "200 kHz",
			"signal_strength":  -45.3,
			"snr":              25.7,
			"modulation":       "FM",
			"timestamp":        "2024-01-01T12:00:00Z",
		},
		"analysis_method": "combined",
	}

	userID, _ := c.Get("user_id")
	h.log.Info("Signal analysis requested by user %v from clients %v", userID, selectedClients)

	c.JSON(http.StatusOK, signalData)
}

// selectType1Clients selects up to 3 Type 1 clients randomly from available connected clients
func (h *Type2Handler) selectType1Clients() ([]int, error) {
	rows, err := h.db.Query(
		"SELECT id FROM type1_clients WHERE status = 'connected' ORDER BY RANDOM() LIMIT 3",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clients []int
	for rows.Next() {
		var clientID int
		if err := rows.Scan(&clientID); err != nil {
			return nil, err
		}
		clients = append(clients, clientID)
	}

	if len(clients) < 3 {
		return nil, sql.ErrNoRows // Not enough clients
	}

	// If we have more than 3, randomly select 3
	if len(clients) > 3 {
		rand.Shuffle(len(clients), func(i, j int) {
			clients[i], clients[j] = clients[j], clients[i]
		})
		clients = clients[:3]
	}

	return clients, nil
}
