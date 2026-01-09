package writer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"

	pb "github.com/fluxo/export-middleware/proto"
	"github.com/xuri/excelize/v2"
)

// ExcelWriter implements Writer interface for Excel format
type ExcelWriter struct {
	file         *excelize.File
	outputPath   string
	sheetName    string
	currentRow   int
	rowCount     int64
	streamWriter *excelize.StreamWriter
}

// NewExcelWriter creates a new Excel writer
func NewExcelWriter() *ExcelWriter {
	return &ExcelWriter{
		sheetName:  "Sheet1",
		currentRow: 1,
	}
}

// Initialize prepares the Excel writer with configuration
func (w *ExcelWriter) Initialize(ctx context.Context, metadata *pb.ExportMetadata, outputPath string) error {
	w.outputPath = outputPath

	// Parse options
	if metadata.Options != nil {
		if metadata.Options.ExcelSheetName != "" {
			w.sheetName = metadata.Options.ExcelSheetName
		}
		if metadata.Options.ExcelStartRow > 0 {
			w.currentRow = int(metadata.Options.ExcelStartRow)
		}
	}

	// Create new Excel file
	w.file = excelize.NewFile()

	// Create or get sheet
	index, err := w.file.NewSheet(w.sheetName)
	if err != nil {
		return fmt.Errorf("failed to create sheet: %w", err)
	}
	w.file.SetActiveSheet(index)

	// Delete default Sheet1 if we created a custom sheet
	if w.sheetName != "Sheet1" {
		if err := w.file.DeleteSheet("Sheet1"); err != nil {
			// Ignore error if Sheet1 doesn't exist
		}
	}

	// Initialize stream writer for better performance
	streamWriter, err := w.file.NewStreamWriter(w.sheetName)
	if err != nil {
		return fmt.Errorf("failed to create stream writer: %w", err)
	}
	w.streamWriter = streamWriter

	return nil
}

// WriteHeader writes the column headers
func (w *ExcelWriter) WriteHeader(columns []*pb.ColumnDefinition) error {
	if w.streamWriter == nil {
		return fmt.Errorf("writer not initialized")
	}

	// Prepare header row
	headers := make([]interface{}, len(columns))
	for i, col := range columns {
		headers[i] = col.Name
	}

	// Write header row
	cell, err := excelize.CoordinatesToCellName(1, w.currentRow)
	if err != nil {
		return fmt.Errorf("failed to get cell coordinate: %w", err)
	}

	if err := w.streamWriter.SetRow(cell, headers); err != nil {
		return fmt.Errorf("failed to write header row: %w", err)
	}

	w.currentRow++
	w.rowCount++

	// Set column widths if specified
	for i, col := range columns {
		if col.Width > 0 {
			colName, err := excelize.ColumnNumberToName(i + 1)
			if err != nil {
				continue
			}
			w.file.SetColWidth(w.sheetName, colName, colName, float64(col.Width))
		}
	}

	return nil
}

// WriteRecords appends data records
func (w *ExcelWriter) WriteRecords(records []*pb.Record) error {
	if w.streamWriter == nil {
		return fmt.Errorf("writer not initialized")
	}

	for _, record := range records {
		// Convert string values to interface{} for excelize
		values := make([]interface{}, len(record.Values))
		for i, val := range record.Values {
			values[i] = val
		}

		// Get cell coordinate for current row
		cell, err := excelize.CoordinatesToCellName(1, w.currentRow)
		if err != nil {
			return fmt.Errorf("failed to get cell coordinate: %w", err)
		}

		// Write row
		if err := w.streamWriter.SetRow(cell, values); err != nil {
			return fmt.Errorf("failed to write record: %w", err)
		}

		w.currentRow++
		w.rowCount++
	}

	// Flush periodically
	if w.rowCount%1000 == 0 {
		if err := w.streamWriter.Flush(); err != nil {
			return fmt.Errorf("failed to flush stream: %w", err)
		}
	}

	return nil
}

// Finalize closes the file and returns metadata
func (w *ExcelWriter) Finalize() (*FileMetadata, error) {
	if w.streamWriter == nil {
		return nil, fmt.Errorf("writer not initialized")
	}

	// Flush stream writer
	if err := w.streamWriter.Flush(); err != nil {
		return nil, fmt.Errorf("failed to flush stream: %w", err)
	}

	// Save file to disk
	if err := w.file.SaveAs(w.outputPath); err != nil {
		return nil, fmt.Errorf("failed to save Excel file: %w", err)
	}

	// Close file
	if err := w.file.Close(); err != nil {
		return nil, fmt.Errorf("failed to close Excel file: %w", err)
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
func (w *ExcelWriter) calculateChecksum() (string, error) {
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
func (w *ExcelWriter) Cleanup() error {
	if w.file != nil {
		w.file.Close()
	}
	if w.outputPath != "" {
		os.Remove(w.outputPath)
	}
	return nil
}
