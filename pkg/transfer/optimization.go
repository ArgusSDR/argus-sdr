package transfer

import (
	"crypto/md5"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"argus-sdr/pkg/compression"
	"argus-sdr/pkg/logger"
)

// TransferOptimizer handles file transfer optimization
type TransferOptimizer struct {
	log                *logger.Logger
	enableCompression  bool
	compressionLevel   compression.CompressionLevel
	verifyChecksums    bool
	maxRetries         int
	retryDelay         time.Duration
}

// OptimizationOptions configures transfer optimization behavior
type OptimizationOptions struct {
	EnableCompression bool                         `json:"enable_compression"`
	CompressionLevel  compression.CompressionLevel `json:"compression_level"`
	VerifyChecksums   bool                         `json:"verify_checksums"`
	MaxRetries        int                          `json:"max_retries"`
	RetryDelay        time.Duration                `json:"retry_delay"`
}

// TransferStats holds statistics about file transfer optimization
type TransferStats struct {
	OriginalSize      int64                         `json:"original_size"`
	TransferSize      int64                         `json:"transfer_size"`
	CompressionStats  *compression.CompressionStats `json:"compression_stats,omitempty"`
	TransferTime      time.Duration                 `json:"transfer_time"`
	Checksum          string                        `json:"checksum"`
	CompressionUsed   bool                          `json:"compression_used"`
	RetryCount        int                           `json:"retry_count"`
	TransferSpeedMBps float64                       `json:"transfer_speed_mbps"`
}

// NewTransferOptimizer creates a new transfer optimizer instance
func NewTransferOptimizer(log *logger.Logger, options OptimizationOptions) *TransferOptimizer {
	if options.MaxRetries <= 0 {
		options.MaxRetries = 3
	}
	if options.RetryDelay <= 0 {
		options.RetryDelay = time.Second
	}

	return &TransferOptimizer{
		log:               log,
		enableCompression: options.EnableCompression,
		compressionLevel:  options.CompressionLevel,
		verifyChecksums:   options.VerifyChecksums,
		maxRetries:        options.MaxRetries,
		retryDelay:        options.RetryDelay,
	}
}

// OptimizeFile prepares a file for optimal transfer
func (to *TransferOptimizer) OptimizeFile(inputPath string) (optimizedPath string, stats *TransferStats, err error) {
	startTime := time.Now()
	
	// Initialize stats
	stats = &TransferStats{
		CompressionUsed: false,
		RetryCount:      0,
	}

	// Get original file size
	originalStat, err := os.Stat(inputPath)
	if err != nil {
		return "", nil, fmt.Errorf("failed to get file stats: %w", err)
	}
	stats.OriginalSize = originalStat.Size()

	// Calculate original file checksum if verification is enabled
	if to.verifyChecksums {
		stats.Checksum, err = to.calculateMD5(inputPath)
		if err != nil {
			return "", nil, fmt.Errorf("failed to calculate checksum: %w", err)
		}
	}

	optimizedPath = inputPath
	stats.TransferSize = stats.OriginalSize

	// Apply compression if enabled and beneficial
	if to.enableCompression {
		shouldCompress, err := compression.EstimateCompressionBenefit(inputPath)
		if err != nil {
			to.log.Warn("Failed to estimate compression benefit for %s: %v", inputPath, err)
		} else if shouldCompress {
			compressedPath, compressionStats, err := to.compressFile(inputPath)
			if err != nil {
				to.log.Warn("Failed to compress file %s: %v", inputPath, err)
			} else if compressionStats.SavingsPercent > 5.0 {
				// Use compressed version if it saves significant space
				optimizedPath = compressedPath
				stats.TransferSize = compressionStats.CompressedSize
				stats.CompressionStats = compressionStats
				stats.CompressionUsed = true
				
				to.log.Info("Compressed %s: %.2f%% size reduction (%d -> %d bytes)", 
					filepath.Base(inputPath), 
					compressionStats.SavingsPercent,
					compressionStats.OriginalSize,
					compressionStats.CompressedSize)
			} else {
				// Remove compressed file if savings are minimal
				os.Remove(compressedPath)
				to.log.Debug("Compression not beneficial for %s (%.2f%% savings)", 
					filepath.Base(inputPath), compressionStats.SavingsPercent)
			}
		}
	}

	// Calculate transfer time and speed
	stats.TransferTime = time.Since(startTime)
	if stats.TransferTime.Seconds() > 0 {
		stats.TransferSpeedMBps = float64(stats.TransferSize) / (1024 * 1024) / stats.TransferTime.Seconds()
	}

	return optimizedPath, stats, nil
}

// compressFile compresses a file and returns the compressed file path
func (to *TransferOptimizer) compressFile(inputPath string) (string, *compression.CompressionStats, error) {
	// Determine compression level
	level := to.compressionLevel
	if level == compression.CompressionLevel(0) {
		// Auto-select compression level based on file size
		stat, err := os.Stat(inputPath)
		if err != nil {
			return "", nil, err
		}
		level = compression.GetOptimalCompressionLevel(stat.Size())
	}

	// Create compressed file path
	compressedPath := inputPath + ".gz"
	
	// Compress file with retries
	var stats *compression.CompressionStats
	var err error
	
	for attempt := 0; attempt <= to.maxRetries; attempt++ {
		stats, err = compression.CompressFile(inputPath, compressedPath, level)
		if err == nil {
			break
		}
		
		if attempt < to.maxRetries {
			to.log.Warn("Compression attempt %d failed for %s: %v (retrying in %v)", 
				attempt+1, filepath.Base(inputPath), err, to.retryDelay)
			time.Sleep(to.retryDelay)
		}
	}

	if err != nil {
		return "", nil, fmt.Errorf("failed to compress after %d attempts: %w", to.maxRetries+1, err)
	}

	return compressedPath, stats, nil
}

// VerifyTransfer verifies the integrity of a transferred file
func (to *TransferOptimizer) VerifyTransfer(originalPath, transferredPath string, wasCompressed bool) error {
	if !to.verifyChecksums {
		return nil // Verification disabled
	}

	var originalChecksum, transferredChecksum string
	var err error

	// Calculate original file checksum
	originalChecksum, err = to.calculateMD5(originalPath)
	if err != nil {
		return fmt.Errorf("failed to calculate original file checksum: %w", err)
	}

	if wasCompressed {
		// If file was compressed, we need to decompress it first to verify
		tempDecompressed := transferredPath + ".verify_temp"
		defer os.Remove(tempDecompressed)

		err = compression.DecompressFile(transferredPath, tempDecompressed)
		if err != nil {
			return fmt.Errorf("failed to decompress for verification: %w", err)
		}

		transferredChecksum, err = to.calculateMD5(tempDecompressed)
		if err != nil {
			return fmt.Errorf("failed to calculate decompressed file checksum: %w", err)
		}
	} else {
		transferredChecksum, err = to.calculateMD5(transferredPath)
		if err != nil {
			return fmt.Errorf("failed to calculate transferred file checksum: %w", err)
		}
	}

	if originalChecksum != transferredChecksum {
		return fmt.Errorf("checksum mismatch: original=%s, transferred=%s", originalChecksum, transferredChecksum)
	}

	to.log.Debug("Transfer verification successful for %s (checksum: %s)", 
		filepath.Base(originalPath), originalChecksum)
	return nil
}

// calculateMD5 calculates MD5 checksum of a file
func (to *TransferOptimizer) calculateMD5(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := md5.New()
	_, err = io.Copy(hash, file)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

// CleanupOptimizedFile removes temporary optimized files
func (to *TransferOptimizer) CleanupOptimizedFile(optimizedPath, originalPath string) error {
	// Only remove if it's a different file than the original (i.e., compressed version)
	if optimizedPath != originalPath {
		err := os.Remove(optimizedPath)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to cleanup optimized file: %w", err)
		}
		to.log.Debug("Cleaned up optimized file: %s", optimizedPath)
	}
	return nil
}

// GetDefaultOptions returns sensible default optimization options
func GetDefaultOptions() OptimizationOptions {
	return OptimizationOptions{
		EnableCompression: true,
		CompressionLevel:  compression.DefaultCompression,
		VerifyChecksums:   true,
		MaxRetries:        3,
		RetryDelay:        time.Second,
	}
}

// GetFastTransferOptions returns options optimized for speed over compression
func GetFastTransferOptions() OptimizationOptions {
	return OptimizationOptions{
		EnableCompression: true,
		CompressionLevel:  compression.BestSpeed,
		VerifyChecksums:   false,
		MaxRetries:        1,
		RetryDelay:        500 * time.Millisecond,
	}
}

// GetHighCompressionOptions returns options optimized for maximum compression
func GetHighCompressionOptions() OptimizationOptions {
	return OptimizationOptions{
		EnableCompression: true,
		CompressionLevel:  compression.BestCompression,
		VerifyChecksums:   true,
		MaxRetries:        5,
		RetryDelay:        2 * time.Second,
	}
}