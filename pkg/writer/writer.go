package writer

import (
	"context"

	pb "github.com/fluxo/export-middleware/proto"
)

// FileMetadata contains metadata about the generated file
type FileMetadata struct {
	Path     string
	Size     int64
	Checksum string
	RowCount int64
}

// Writer defines the interface that all format writers must implement
type Writer interface {
	// Initialize prepares the writer with configuration
	Initialize(ctx context.Context, metadata *pb.ExportMetadata, outputPath string) error

	// WriteHeader writes the column headers
	WriteHeader(columns []*pb.ColumnDefinition) error

	// WriteRecords appends data records
	WriteRecords(records []*pb.Record) error

	// Finalize closes the file and returns metadata
	Finalize() (*FileMetadata, error)

	// Cleanup releases resources on error
	Cleanup() error
}
