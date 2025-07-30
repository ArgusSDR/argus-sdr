package compression

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// CompressionLevel defines compression levels
type CompressionLevel int

const (
	NoCompression      CompressionLevel = gzip.NoCompression
	BestSpeed          CompressionLevel = gzip.BestSpeed
	BestCompression    CompressionLevel = gzip.BestCompression
	DefaultCompression CompressionLevel = gzip.DefaultCompression
)

// CompressionStats holds statistics about compression operation
type CompressionStats struct {
	OriginalSize   int64   `json:"original_size"`
	CompressedSize int64   `json:"compressed_size"`
	CompressionRatio float64 `json:"compression_ratio"`
	SavingsPercent   float64 `json:"savings_percent"`
}

// CompressFile compresses a file using gzip compression
func CompressFile(inputPath, outputPath string, level CompressionLevel) (*CompressionStats, error) {
	// Open input file
	inputFile, err := os.Open(inputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open input file: %w", err)
	}
	defer inputFile.Close()

	// Get input file stats
	inputStat, err := inputFile.Stat()
	if err != nil {
		return nil, fmt.Errorf("failed to get input file stats: %w", err)
	}
	originalSize := inputStat.Size()

	// Create output file
	outputFile, err := os.Create(outputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create output file: %w", err)
	}
	defer outputFile.Close()

	// Create gzip writer
	gzipWriter, err := gzip.NewWriterLevel(outputFile, int(level))
	if err != nil {
		return nil, fmt.Errorf("failed to create gzip writer: %w", err)
	}
	defer gzipWriter.Close()

	// Set gzip header
	gzipWriter.Name = filepath.Base(inputPath)

	// Copy data with compression
	_, err = io.Copy(gzipWriter, inputFile)
	if err != nil {
		return nil, fmt.Errorf("failed to compress data: %w", err)
	}

	// Close gzip writer to ensure all data is written
	if err := gzipWriter.Close(); err != nil {
		return nil, fmt.Errorf("failed to close gzip writer: %w", err)
	}

	// Get output file size
	outputStat, err := outputFile.Stat()
	if err != nil {
		return nil, fmt.Errorf("failed to get output file stats: %w", err)
	}
	compressedSize := outputStat.Size()

	// Calculate compression statistics
	stats := &CompressionStats{
		OriginalSize:   originalSize,
		CompressedSize: compressedSize,
	}

	if originalSize > 0 {
		stats.CompressionRatio = float64(compressedSize) / float64(originalSize)
		stats.SavingsPercent = (1.0 - stats.CompressionRatio) * 100.0
	}

	return stats, nil
}

// DecompressFile decompresses a gzip file
func DecompressFile(inputPath, outputPath string) error {
	// Open compressed input file
	inputFile, err := os.Open(inputPath)
	if err != nil {
		return fmt.Errorf("failed to open compressed file: %w", err)
	}
	defer inputFile.Close()

	// Create gzip reader
	gzipReader, err := gzip.NewReader(inputFile)
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gzipReader.Close()

	// Create output file
	outputFile, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer outputFile.Close()

	// Copy decompressed data
	_, err = io.Copy(outputFile, gzipReader)
	if err != nil {
		return fmt.Errorf("failed to decompress data: %w", err)
	}

	return nil
}

// CompressFileInPlace compresses a file and replaces the original with compressed version
func CompressFileInPlace(filePath string, level CompressionLevel) (*CompressionStats, error) {
	tempPath := filePath + ".tmp.gz"
	
	// Compress to temporary file
	stats, err := CompressFile(filePath, tempPath, level)
	if err != nil {
		return nil, err
	}

	// Only replace if compression actually saves space (> 5% reduction)
	if stats.SavingsPercent > 5.0 {
		// Remove original file
		if err := os.Remove(filePath); err != nil {
			os.Remove(tempPath) // Clean up temp file
			return nil, fmt.Errorf("failed to remove original file: %w", err)
		}

		// Rename compressed file to original name with .gz extension
		compressedPath := filePath + ".gz"
		if err := os.Rename(tempPath, compressedPath); err != nil {
			return nil, fmt.Errorf("failed to rename compressed file: %w", err)
		}

		return stats, nil
	} else {
		// Compression not beneficial, remove temp file
		os.Remove(tempPath)
		return &CompressionStats{
			OriginalSize:     stats.OriginalSize,
			CompressedSize:   stats.OriginalSize,
			CompressionRatio: 1.0,
			SavingsPercent:   0.0,
		}, nil
	}
}

// IsCompressed checks if a file is already compressed based on extension
func IsCompressed(filePath string) bool {
	ext := strings.ToLower(filepath.Ext(filePath))
	compressedExts := []string{".gz", ".gzip", ".bz2", ".zip", ".tar.gz", ".tgz"}
	
	for _, compressedExt := range compressedExts {
		if ext == compressedExt {
			return true
		}
	}
	return false
}

// GetOptimalCompressionLevel determines optimal compression level based on file size
func GetOptimalCompressionLevel(fileSize int64) CompressionLevel {
	// For small files (< 1MB), use best compression for maximum space savings
	if fileSize < 1024*1024 {
		return BestCompression
	}
	
	// For medium files (1MB - 10MB), use default compression for balance
	if fileSize < 10*1024*1024 {
		return DefaultCompression
	}
	
	// For large files (> 10MB), use best speed to reduce processing time
	return BestSpeed
}

// EstimateCompressionBenefit estimates if compression would be beneficial
func EstimateCompressionBenefit(filePath string) (bool, error) {
	// Check if already compressed
	if IsCompressed(filePath) {
		return false, nil
	}

	// Get file info
	stat, err := os.Stat(filePath)
	if err != nil {
		return false, err
	}

	// Don't compress very small files (< 1KB)
	if stat.Size() < 1024 {
		return false, nil
	}

	// Check file type - some files don't compress well
	ext := strings.ToLower(filepath.Ext(filePath))
	nonCompressibleExts := []string{".jpg", ".jpeg", ".png", ".gif", ".mp3", ".mp4", ".avi", ".zip", ".rar"}
	
	for _, nonCompressibleExt := range nonCompressibleExts {
		if ext == nonCompressibleExt {
			return false, nil
		}
	}

	// Text files, data files, and logs typically compress well
	compressibleExts := []string{".txt", ".log", ".csv", ".json", ".xml", ".dat", ".raw"}
	for _, compressibleExt := range compressibleExts {
		if ext == compressibleExt {
			return true, nil
		}
	}

	// For unknown file types, compress if file is reasonably large
	return stat.Size() > 10*1024, nil
}