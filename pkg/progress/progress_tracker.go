package progress

import (
	"sync"
	"time"

	"argus-sdr/pkg/logger"
)

// TransferProgress represents the progress of a file transfer
type TransferProgress struct {
	ID               string        `json:"id"`
	RequestID        string        `json:"request_id"`
	StationID        string        `json:"station_id"`
	Status           string        `json:"status"` // pending, processing, transferring, completed, failed
	StartTime        time.Time     `json:"start_time"`
	LastUpdate       time.Time     `json:"last_update"`
	TotalBytes       int64         `json:"total_bytes"`
	TransferredBytes int64         `json:"transferred_bytes"`
	ProgressPercent  float64       `json:"progress_percent"`
	TransferRate     float64       `json:"transfer_rate_mbps"`
	EstimatedETA     time.Duration `json:"estimated_eta"`
	ErrorMessage     string        `json:"error_message,omitempty"`
	Metadata         map[string]interface{} `json:"metadata,omitempty"`
}

// ProgressTracker manages progress tracking for multiple transfers
type ProgressTracker struct {
	log       *logger.Logger
	transfers map[string]*TransferProgress
	mutex     sync.RWMutex
}

// NewProgressTracker creates a new progress tracker
func NewProgressTracker(log *logger.Logger) *ProgressTracker {
	return &ProgressTracker{
		log:       log,
		transfers: make(map[string]*TransferProgress),
	}
}

// StartTracking begins tracking progress for a new transfer
func (pt *ProgressTracker) StartTracking(id, requestID, stationID string, totalBytes int64) *TransferProgress {
	pt.mutex.Lock()
	defer pt.mutex.Unlock()

	progress := &TransferProgress{
		ID:               id,
		RequestID:        requestID,
		StationID:        stationID,
		Status:           "pending",
		StartTime:        time.Now(),
		LastUpdate:       time.Now(),
		TotalBytes:       totalBytes,
		TransferredBytes: 0,
		ProgressPercent:  0.0,
		TransferRate:     0.0,
		EstimatedETA:     0,
		Metadata:         make(map[string]interface{}),
	}

	pt.transfers[id] = progress
	pt.log.Debug("Started tracking progress for transfer %s (request: %s, station: %s)", 
		id, requestID, stationID)
	
	return progress
}

// UpdateProgress updates the progress of a transfer
func (pt *ProgressTracker) UpdateProgress(id string, transferredBytes int64) error {
	pt.mutex.Lock()
	defer pt.mutex.Unlock()

	progress, exists := pt.transfers[id]
	if !exists {
		pt.log.Warn("Attempted to update progress for unknown transfer: %s", id)
		return nil // Don't error, just ignore
	}

	now := time.Now()
	timeDiff := now.Sub(progress.LastUpdate)
	bytesDiff := transferredBytes - progress.TransferredBytes

	// Update basic progress
	progress.TransferredBytes = transferredBytes
	progress.LastUpdate = now

	// Calculate progress percentage
	if progress.TotalBytes > 0 {
		progress.ProgressPercent = (float64(transferredBytes) / float64(progress.TotalBytes)) * 100.0
	}

	// Calculate transfer rate (MB/s)
	if timeDiff.Seconds() > 0 && bytesDiff > 0 {
		progress.TransferRate = float64(bytesDiff) / (1024 * 1024) / timeDiff.Seconds()
	}

	// Estimate time remaining
	if progress.TransferRate > 0 && progress.TotalBytes > transferredBytes {
		remainingBytes := progress.TotalBytes - transferredBytes
		remainingMB := float64(remainingBytes) / (1024 * 1024)
		progress.EstimatedETA = time.Duration(remainingMB/progress.TransferRate) * time.Second
	}

	pt.log.Debug("Updated progress for transfer %s: %.1f%% (%d/%d bytes, %.2f MB/s)", 
		id, progress.ProgressPercent, transferredBytes, progress.TotalBytes, progress.TransferRate)

	return nil
}

// SetStatus updates the status of a transfer
func (pt *ProgressTracker) SetStatus(id, status string) {
	pt.mutex.Lock()
	defer pt.mutex.Unlock()

	progress, exists := pt.transfers[id]
	if !exists {
		return
	}

	progress.Status = status
	progress.LastUpdate = time.Now()

	pt.log.Info("Transfer %s status changed to: %s", id, status)
}

// SetError sets an error message for a transfer
func (pt *ProgressTracker) SetError(id, errorMessage string) {
	pt.mutex.Lock()
	defer pt.mutex.Unlock()

	progress, exists := pt.transfers[id]
	if !exists {
		return
	}

	progress.Status = "failed"
	progress.ErrorMessage = errorMessage
	progress.LastUpdate = time.Now()

	pt.log.Error("Transfer %s failed: %s", id, errorMessage)
}

// CompleteTransfer marks a transfer as completed
func (pt *ProgressTracker) CompleteTransfer(id string) {
	pt.mutex.Lock()
	defer pt.mutex.Unlock()

	progress, exists := pt.transfers[id]
	if !exists {
		return
	}

	progress.Status = "completed"
	progress.ProgressPercent = 100.0
	progress.LastUpdate = time.Now()
	progress.EstimatedETA = 0

	duration := time.Since(progress.StartTime)
	avgRate := float64(progress.TotalBytes) / (1024 * 1024) / duration.Seconds()

	pt.log.Info("Transfer %s completed: %d bytes in %v (avg %.2f MB/s)", 
		id, progress.TotalBytes, duration, avgRate)
}

// GetProgress returns the current progress of a transfer
func (pt *ProgressTracker) GetProgress(id string) (*TransferProgress, bool) {
	pt.mutex.RLock()
	defer pt.mutex.RUnlock()

	progress, exists := pt.transfers[id]
	if !exists {
		return nil, false
	}

	// Return a copy to avoid race conditions
	progressCopy := *progress
	return &progressCopy, true
}

// GetProgressByRequest returns all transfers for a specific request
func (pt *ProgressTracker) GetProgressByRequest(requestID string) []*TransferProgress {
	pt.mutex.RLock()
	defer pt.mutex.RUnlock()

	var results []*TransferProgress
	for _, progress := range pt.transfers {
		if progress.RequestID == requestID {
			progressCopy := *progress
			results = append(results, &progressCopy)
		}
	}

	return results
}

// GetAllProgress returns all active transfers
func (pt *ProgressTracker) GetAllProgress() []*TransferProgress {
	pt.mutex.RLock()
	defer pt.mutex.RUnlock()

	results := make([]*TransferProgress, 0, len(pt.transfers))
	for _, progress := range pt.transfers {
		progressCopy := *progress
		results = append(results, &progressCopy)
	}

	return results
}

// RemoveProgress removes a completed or failed transfer from tracking
func (pt *ProgressTracker) RemoveProgress(id string) {
	pt.mutex.Lock()
	defer pt.mutex.Unlock()

	delete(pt.transfers, id)
	pt.log.Debug("Removed progress tracking for transfer %s", id)
}

// CleanupOldProgress removes transfers older than the specified duration
func (pt *ProgressTracker) CleanupOldProgress(maxAge time.Duration) int {
	pt.mutex.Lock()
	defer pt.mutex.Unlock()

	cutoff := time.Now().Add(-maxAge)
	removed := 0

	for id, progress := range pt.transfers {
		if progress.LastUpdate.Before(cutoff) && 
		   (progress.Status == "completed" || progress.Status == "failed") {
			delete(pt.transfers, id)
			removed++
		}
	}

	if removed > 0 {
		pt.log.Info("Cleaned up %d old progress entries", removed)
	}

	return removed
}

// GetStats returns overall transfer statistics
func (pt *ProgressTracker) GetStats() TransferStats {
	pt.mutex.RLock()
	defer pt.mutex.RUnlock()

	stats := TransferStats{}
	
	for _, progress := range pt.transfers {
		stats.TotalTransfers++
		
		switch progress.Status {
		case "pending":
			stats.PendingTransfers++
		case "processing":
			stats.ProcessingTransfers++
		case "transferring":
			stats.ActiveTransfers++
		case "completed":
			stats.CompletedTransfers++
			stats.TotalBytesTransferred += progress.TotalBytes
		case "failed":
			stats.FailedTransfers++
		}

		if progress.TransferRate > stats.MaxTransferRate {
			stats.MaxTransferRate = progress.TransferRate
		}
	}

	// Calculate average transfer rate for active transfers
	activeCount := 0
	totalRate := 0.0
	for _, progress := range pt.transfers {
		if progress.Status == "transferring" && progress.TransferRate > 0 {
			totalRate += progress.TransferRate
			activeCount++
		}
	}
	
	if activeCount > 0 {
		stats.AvgTransferRate = totalRate / float64(activeCount)
	}

	return stats
}

// TransferStats holds overall transfer statistics
type TransferStats struct {
	TotalTransfers         int     `json:"total_transfers"`
	PendingTransfers       int     `json:"pending_transfers"`
	ProcessingTransfers    int     `json:"processing_transfers"`
	ActiveTransfers        int     `json:"active_transfers"`
	CompletedTransfers     int     `json:"completed_transfers"`
	FailedTransfers        int     `json:"failed_transfers"`
	TotalBytesTransferred  int64   `json:"total_bytes_transferred"`
	MaxTransferRate        float64 `json:"max_transfer_rate_mbps"`
	AvgTransferRate        float64 `json:"avg_transfer_rate_mbps"`
}

// SetMetadata sets custom metadata for a transfer
func (pt *ProgressTracker) SetMetadata(id, key string, value interface{}) {
	pt.mutex.Lock()
	defer pt.mutex.Unlock()

	progress, exists := pt.transfers[id]
	if !exists {
		return
	}

	progress.Metadata[key] = value
	progress.LastUpdate = time.Now()
}