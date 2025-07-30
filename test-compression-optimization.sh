#!/bin/bash

# Test File Compression and Transfer Optimization
# This script tests the new compression and transfer optimization features

echo "🗜️  Testing File Compression and Transfer Optimization"
echo "====================================================="
echo

# Create test data directory
mkdir -p test_data
cd test_data

# Create various test files to demonstrate compression
echo "Creating test files..."

# 1. Text file (should compress well)
echo "Creating large text file..."
for i in {1..1000}; do
    echo "This is line $i with some repetitive content that should compress well. Lorem ipsum dolor sit amet, consectetur adipiscing elit." >> large_text.txt
done

# 2. Random binary data (should not compress well)
echo "Creating random binary file..."
dd if=/dev/urandom of=random_binary.dat bs=1024 count=100 2>/dev/null

# 3. Small file (should not be compressed)
echo "Creating small file..."
echo "Small file content" > small_file.txt

# 4. Already compressed file (should be skipped)
echo "Creating already compressed file..."
echo "Some content for compression" | gzip > already_compressed.gz

echo "Test files created:"
ls -lh
echo

# Test compression library
echo "Testing compression library..."
cd ..

# Create Go test program
cat > test_compression.go << 'EOF'
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"argus-sdr/pkg/compression"
	"argus-sdr/pkg/transfer"
	"argus-sdr/pkg/logger"
)

func main() {
	log := logger.New()
	
	// Test compression on different file types
	testFiles := []string{
		"test_data/large_text.txt",
		"test_data/random_binary.dat", 
		"test_data/small_file.txt",
		"test_data/already_compressed.gz",
	}

	fmt.Println("=== Compression Test Results ===")
	fmt.Println()

	for _, file := range testFiles {
		fmt.Printf("Testing file: %s\n", filepath.Base(file))
		
		// Check if file exists
		if _, err := os.Stat(file); os.IsNotExist(err) {
			fmt.Printf("  ❌ File not found: %s\n\n", file)
			continue
		}

		// Get file size
		stat, _ := os.Stat(file)
		fmt.Printf("  Original size: %d bytes\n", stat.Size())

		// Test compression benefit estimation
		beneficial, err := compression.EstimateCompressionBenefit(file)
		if err != nil {
			fmt.Printf("  ❌ Error estimating benefit: %v\n\n", err)
			continue
		}
		fmt.Printf("  Compression beneficial: %v\n", beneficial)

		if beneficial {
			// Test actual compression
			compressedFile := file + ".test.gz"
			level := compression.GetOptimalCompressionLevel(stat.Size())
			fmt.Printf("  Optimal compression level: %v\n", level)

			stats, err := compression.CompressFile(file, compressedFile, level)
			if err != nil {
				fmt.Printf("  ❌ Compression failed: %v\n\n", err)
				continue
			}

			fmt.Printf("  Compressed size: %d bytes\n", stats.CompressedSize)
			fmt.Printf("  Compression ratio: %.2f\n", stats.CompressionRatio)
			fmt.Printf("  Space savings: %.1f%%\n", stats.SavingsPercent)

			// Test transfer optimizer
			optimizer := transfer.NewTransferOptimizer(log, transfer.GetDefaultOptions())
			optimizedPath, transferStats, err := optimizer.OptimizeFile(file)
			if err != nil {
				fmt.Printf("  ❌ Transfer optimization failed: %v\n", err)
			} else {
				fmt.Printf("  Transfer optimization: %v\n", transferStats.CompressionUsed)
				fmt.Printf("  Transfer size: %d bytes\n", transferStats.TransferSize)
				fmt.Printf("  Transfer speed: %.2f MB/s\n", transferStats.TransferSpeedMBps)
				
				// Cleanup
				optimizer.CleanupOptimizedFile(optimizedPath, file)
			}

			// Cleanup test compressed file
			os.Remove(compressedFile)
		} else {
			fmt.Printf("  ⏭️  Skipping compression (not beneficial)\n")
		}
		fmt.Println()
	}

	fmt.Println("=== Transfer Optimization Options ===")
	fmt.Println()

	defaultOpts := transfer.GetDefaultOptions()
	fmt.Printf("Default options:\n")
	fmt.Printf("  Compression enabled: %v\n", defaultOpts.EnableCompression)
	fmt.Printf("  Compression level: %v\n", defaultOpts.CompressionLevel)
	fmt.Printf("  Verify checksums: %v\n", defaultOpts.VerifyChecksums)
	fmt.Printf("  Max retries: %d\n", defaultOpts.MaxRetries)
	fmt.Println()

	fastOpts := transfer.GetFastTransferOptions()
	fmt.Printf("Fast transfer options:\n")
	fmt.Printf("  Compression enabled: %v\n", fastOpts.EnableCompression)
	fmt.Printf("  Compression level: %v\n", fastOpts.CompressionLevel)
	fmt.Printf("  Verify checksums: %v\n", fastOpts.VerifyChecksums)
	fmt.Printf("  Max retries: %d\n", fastOpts.MaxRetries)
	fmt.Println()

	highCompOpts := transfer.GetHighCompressionOptions()
	fmt.Printf("High compression options:\n")
	fmt.Printf("  Compression enabled: %v\n", highCompOpts.EnableCompression)
	fmt.Printf("  Compression level: %v\n", highCompOpts.CompressionLevel)
	fmt.Printf("  Verify checksums: %v\n", highCompOpts.VerifyChecksums)
	fmt.Printf("  Max retries: %d\n", highCompOpts.MaxRetries)
}
EOF

# Build and run test
echo "Building compression test..."
go build -o test_compression test_compression.go

if [ $? -eq 0 ]; then
    echo "Running compression tests..."
    ./test_compression
else
    echo "❌ Failed to build compression test"
fi

# Cleanup
echo
echo "Cleaning up test files..."
rm -f test_compression test_compression.go
rm -rf test_data

echo "✅ Compression and transfer optimization tests completed!"
echo
echo "Key features implemented:"
echo "- Automatic compression benefit estimation"
echo "- Multiple compression levels (speed vs. size trade-offs)"
echo "- File type awareness (skip already compressed files)"
echo "- Transfer optimization with checksums and retries"
echo "- Optimized download endpoint with compression headers"