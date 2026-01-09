package writer

import (
	"context"
	"os"
	"testing"

	pb "github.com/fluxo/export-middleware/proto"
)

func TestCSVWriter_BasicExport(t *testing.T) {
	// Create temp directory
	tempDir := t.TempDir()
	outputPath := tempDir + "/test_export.csv"

	// Create writer
	writer := NewCSVWriter()

	// Prepare metadata
	metadata := &pb.ExportMetadata{
		RequestId: "test-001",
		Format:    pb.ExportFormat_FORMAT_CSV,
		Filename:  "test_export.csv",
		Columns: []*pb.ColumnDefinition{
			{Name: "ID", DataType: pb.DataType_DATA_TYPE_NUMBER},
			{Name: "Name", DataType: pb.DataType_DATA_TYPE_STRING},
			{Name: "Email", DataType: pb.DataType_DATA_TYPE_STRING},
		},
	}

	// Initialize writer
	ctx := context.Background()
	if err := writer.Initialize(ctx, metadata, outputPath); err != nil {
		t.Fatalf("Failed to initialize writer: %v", err)
	}

	// Write header
	if err := writer.WriteHeader(metadata.Columns); err != nil {
		t.Fatalf("Failed to write header: %v", err)
	}

	// Write records
	records := []*pb.Record{
		{Values: []string{"1", "Alice", "alice@example.com"}},
		{Values: []string{"2", "Bob", "bob@example.com"}},
		{Values: []string{"3", "Charlie", "charlie@example.com"}},
	}

	if err := writer.WriteRecords(records); err != nil {
		t.Fatalf("Failed to write records: %v", err)
	}

	// Finalize
	fileMetadata, err := writer.Finalize()
	if err != nil {
		t.Fatalf("Failed to finalize: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		t.Fatalf("Output file does not exist")
	}

	// Verify metadata
	if fileMetadata.RowCount != 4 { // header + 3 records
		t.Errorf("Expected 4 rows, got %d", fileMetadata.RowCount)
	}

	if fileMetadata.Size == 0 {
		t.Error("File size should not be zero")
	}

	if fileMetadata.Checksum == "" {
		t.Error("Checksum should not be empty")
	}

	// Read and verify content
	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("Failed to read output file: %v", err)
	}

	expectedContent := "ID,Name,Email\n1,Alice,alice@example.com\n2,Bob,bob@example.com\n3,Charlie,charlie@example.com\n"
	if string(content) != expectedContent {
		t.Errorf("Content mismatch.\nExpected:\n%s\nGot:\n%s", expectedContent, string(content))
	}
}

func TestCSVWriter_CustomDelimiter(t *testing.T) {
	tempDir := t.TempDir()
	outputPath := tempDir + "/test_export_tab.csv"

	writer := NewCSVWriter()

	metadata := &pb.ExportMetadata{
		RequestId: "test-002",
		Format:    pb.ExportFormat_FORMAT_CSV,
		Filename:  "test_export.csv",
		Columns: []*pb.ColumnDefinition{
			{Name: "Col1", DataType: pb.DataType_DATA_TYPE_STRING},
			{Name: "Col2", DataType: pb.DataType_DATA_TYPE_STRING},
		},
		Options: &pb.FormatOptions{
			CsvDelimiter: "\t",
		},
	}

	ctx := context.Background()
	if err := writer.Initialize(ctx, metadata, outputPath); err != nil {
		t.Fatalf("Failed to initialize writer: %v", err)
	}

	if err := writer.WriteHeader(metadata.Columns); err != nil {
		t.Fatalf("Failed to write header: %v", err)
	}

	records := []*pb.Record{
		{Values: []string{"A", "B"}},
	}

	if err := writer.WriteRecords(records); err != nil {
		t.Fatalf("Failed to write records: %v", err)
	}

	if _, err := writer.Finalize(); err != nil {
		t.Fatalf("Failed to finalize: %v", err)
	}

	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("Failed to read output file: %v", err)
	}

	expectedContent := "Col1\tCol2\nA\tB\n"
	if string(content) != expectedContent {
		t.Errorf("Content mismatch.\nExpected:\n%s\nGot:\n%s", expectedContent, string(content))
	}
}

func TestCSVWriter_SpecialCharacters(t *testing.T) {
	tempDir := t.TempDir()
	outputPath := tempDir + "/test_special.csv"

	writer := NewCSVWriter()

	metadata := &pb.ExportMetadata{
		RequestId: "test-003",
		Format:    pb.ExportFormat_FORMAT_CSV,
		Filename:  "test_special.csv",
		Columns: []*pb.ColumnDefinition{
			{Name: "Text", DataType: pb.DataType_DATA_TYPE_STRING},
		},
	}

	ctx := context.Background()
	if err := writer.Initialize(ctx, metadata, outputPath); err != nil {
		t.Fatalf("Failed to initialize writer: %v", err)
	}

	if err := writer.WriteHeader(metadata.Columns); err != nil {
		t.Fatalf("Failed to write header: %v", err)
	}

	// Test with quotes, commas, and newlines
	records := []*pb.Record{
		{Values: []string{"Hello, World"}},
		{Values: []string{`Text with "quotes"`}},
		{Values: []string{"Text\nwith\nnewlines"}},
	}

	if err := writer.WriteRecords(records); err != nil {
		t.Fatalf("Failed to write records: %v", err)
	}

	if _, err := writer.Finalize(); err != nil {
		t.Fatalf("Failed to finalize: %v", err)
	}

	// Verify file was created successfully
	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		t.Fatalf("Output file does not exist")
	}
}
