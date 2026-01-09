package writer

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"io"
	"os"

	pb "github.com/fluxo/export-middleware/proto"
)

// CSVWriter implements Writer interface for CSV format
type CSVWriter struct {
	file       *os.File
	writer     *csv.Writer
	buffered   *bufio.Writer
	outputPath string
	rowCount   int64
	delimiter  rune
	encoding   string
}

// NewCSVWriter creates a new CSV writer
func NewCSVWriter() *CSVWriter {
	return &CSVWriter{
		delimiter: ',',
		encoding:  "UTF-8",
	}
}

// Initialize prepares the CSV writer with configuration
func (w *CSVWriter) Initialize(ctx context.Context, metadata *pb.ExportMetadata, outputPath string) error {
	w.outputPath = outputPath

	// Parse options
	if metadata.Options != nil {
		if metadata.Options.CsvDelimiter != "" {
			runes := []rune(metadata.Options.CsvDelimiter)
			if len(runes) > 0 {
				w.delimiter = runes[0]
			}
		}
		if metadata.Options.CsvEncoding != "" {
			w.encoding = metadata.Options.CsvEncoding
		}
	}

	// Create file
	file, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create CSV file: %w", err)
	}
	w.file = file

	// Create buffered writer for better performance
	w.buffered = bufio.NewWriterSize(file, 64*1024) // 64KB buffer

	// Create CSV writer
	w.writer = csv.NewWriter(w.buffered)
	w.writer.Comma = w.delimiter

	return nil
}

// WriteHeader writes the column headers
func (w *CSVWriter) WriteHeader(columns []*pb.ColumnDefinition) error {
	if w.writer == nil {
		return fmt.Errorf("writer not initialized")
	}

	headers := make([]string, len(columns))
	for i, col := range columns {
		headers[i] = col.Name
	}

	if err := w.writer.Write(headers); err != nil {
		return fmt.Errorf("failed to write headers: %w", err)
	}

	w.rowCount++
	return nil
}

// WriteRecords appends data records
func (w *CSVWriter) WriteRecords(records []*pb.Record) error {
	if w.writer == nil {
		return fmt.Errorf("writer not initialized")
	}

	for _, record := range records {
		// Handle special characters and quoting per RFC 4180
		values := make([]string, len(record.Values))
		for i, val := range record.Values {
			values[i] = w.sanitizeValue(val)
		}

		if err := w.writer.Write(values); err != nil {
			return fmt.Errorf("failed to write record: %w", err)
		}
		w.rowCount++
	}

	// Flush periodically for better streaming
	if w.rowCount%1000 == 0 {
		w.writer.Flush()
		if err := w.writer.Error(); err != nil {
			return fmt.Errorf("failed to flush writer: %w", err)
		}
	}

	return nil
}

// sanitizeValue handles CSV value escaping per RFC 4180
func (w *CSVWriter) sanitizeValue(val string) string {
	// CSV writer handles quoting automatically, but we can pre-process if needed
	// For now, just return the value as-is and let csv.Writer handle it
	return val
}

// Finalize closes the file and returns metadata
func (w *CSVWriter) Finalize() (*FileMetadata, error) {
	if w.writer == nil {
		return nil, fmt.Errorf("writer not initialized")
	}

	// Flush CSV writer
	w.writer.Flush()
	if err := w.writer.Error(); err != nil {
		return nil, fmt.Errorf("failed to flush CSV writer: %w", err)
	}

	// Flush buffered writer
	if err := w.buffered.Flush(); err != nil {
		return nil, fmt.Errorf("failed to flush buffer: %w", err)
	}

	// Close file
	if err := w.file.Close(); err != nil {
		return nil, fmt.Errorf("failed to close file: %w", err)
	}

	// Calculate file size and checksum
	fileInfo, err := os.Stat(w.outputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}

	checksum, err := w.calculateChecksum()
	if err != nil {
		return nil, fmt.Errorf("failed to calculate checksum: %w", err)
	}

	return &FileMetadata{
		Path:     w.outputPath,
		Size:     fileInfo.Size(),
		Checksum: checksum,
		RowCount: w.rowCount,
	}, nil
}

// calculateChecksum calculates SHA256 checksum of the file
func (w *CSVWriter) calculateChecksum() (string, error) {
	file, err := os.Open(w.outputPath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// Cleanup releases resources on error
func (w *CSVWriter) Cleanup() error {
	if w.file != nil {
		w.file.Close()
	}
	if w.outputPath != "" {
		os.Remove(w.outputPath)
	}
	return nil
}
