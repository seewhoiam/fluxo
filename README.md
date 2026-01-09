# Export Middleware

A high-performance data export middleware service built with Go that enables PHP systems to offload resource-intensive Excel and CSV generation tasks.

## Features

- 📊 **Streaming Export**: Handle 500k-800k records with minimal memory footprint (<100MB)
- 🚀 **High Performance**: 10x faster than traditional PHP-based export
- ☁️ **Cloud Storage**: Automatic upload to Alibaba Cloud OSS with signed URLs
- 🔄 **Concurrent Tasks**: Support for 10+ simultaneous exports with task queuing
- 📈 **Progress Tracking**: Real-time status queries with progress percentage
- 📝 **Structured Logging**: Comprehensive JSON logs for troubleshooting
- 🎯 **Format Support**: Excel (.xlsx) and CSV with configurable options
- 🔒 **Production Ready**: Health checks, metrics, and graceful shutdown

## Architecture

```
PHP Client → gRPC Stream → Task Manager → Format Writer → Temp Storage → OSS Upload → Signed URL
                                 ↓
                            Status Query API
```

## Quick Start

### Prerequisites

- Go 1.24+ (due to gRPC dependencies)
- Protocol Buffers compiler (protoc)
- Alibaba Cloud OSS account and credentials

### Installation

```bash
# Clone the repository
git clone https://github.com/fluxo/export-middleware.git
cd export-middleware

# Install dependencies
go mod download

# Generate protobuf code (if needed)
protoc --go_out=. --go_opt=paths=source_relative \
  --go-grpc_out=. --go-grpc_opt=paths=source_relative \
  proto/export.proto

# Build the service
go build -o bin/export-server cmd/server/main.go
```

### Configuration

1. Copy the example configuration:
```bash
cp config.example.yaml config.yaml
```

2. Edit `config.yaml` and configure your OSS credentials:
```yaml
oss:
  endpoint: oss-cn-hangzhou.aliyuncs.com
  bucket: your-bucket-name
  access_key_id: YOUR_ACCESS_KEY_ID
  access_key_secret: YOUR_ACCESS_KEY_SECRET
```

3. Or use environment variables:
```bash
export OSS_ENDPOINT=oss-cn-hangzhou.aliyuncs.com
export OSS_BUCKET=your-bucket-name
export OSS_ACCESS_KEY_ID=your-access-key-id
export OSS_ACCESS_KEY_SECRET=your-access-key-secret
```

### Running the Service

```bash
# Run with default config
./bin/export-server

# Run with custom config
./bin/export-server -config=/path/to/config.yaml

# Run with Docker
docker-compose up -d
```

The service exposes:
- gRPC server on port `9090`
- Status query API on port `9091`
- Metrics endpoint on port `8080`

## Usage

### PHP Client Example

```php
<?php
require 'vendor/autoload.php';

use Export\ExportServiceClient;
use Export\ExportRequest;
use Export\ExportMetadata;
use Export\ColumnDefinition;
use Export\DataBatch;
use Export\Record;

// Create gRPC client
$client = new ExportServiceClient('localhost:9090', [
    'credentials' => Grpc\ChannelCredentials::createInsecure()
]);

// Open streaming export
$call = $client->StreamExport();

// Send metadata
$metadata = new ExportMetadata([
    'request_id' => uniqid(),
    'format' => Export\ExportFormat::FORMAT_CSV,
    'filename' => 'export_' . date('Y-m-d') . '.csv',
    'columns' => [
        new ColumnDefinition(['name' => 'ID', 'data_type' => Export\DataType::DATA_TYPE_NUMBER]),
        new ColumnDefinition(['name' => 'Name', 'data_type' => Export\DataType::DATA_TYPE_STRING]),
        new ColumnDefinition(['name' => 'Email', 'data_type' => Export\DataType::DATA_TYPE_STRING]),
    ]
]);

$request = new ExportRequest(['metadata' => $metadata]);
$call->write($request);

// Receive task ID
list($response, $status) = $call->read();
$taskId = $response->getTaskId();
echo "Task ID: $taskId\n";

// Stream data in batches
$batchSize = 1000;
$sequence = 1;

// Fetch data from database
$stmt = $pdo->query("SELECT id, name, email FROM users");
$batch = [];

while ($row = $stmt->fetch()) {
    $batch[] = new Record([
        'values' => [$row['id'], $row['name'], $row['email']]
    ]);
    
    if (count($batch) >= $batchSize) {
        $dataBatch = new DataBatch([
            'records' => $batch,
            'batch_sequence' => $sequence++
        ]);
        $call->write(new ExportRequest(['batch' => $dataBatch]));
        $batch = [];
    }
}

// Send remaining records
if (!empty($batch)) {
    $dataBatch = new DataBatch([
        'records' => $batch,
        'batch_sequence' => $sequence
    ]);
    $call->write(new ExportRequest(['batch' => $dataBatch]));
}

// Close stream and get result
$call->writesDone();
list($finalResponse, $status) = $call->read();

if ($status->code === Grpc\STATUS_OK) {
    echo "Export completed!\n";
    echo "OSS URL: " . $finalResponse->getOssUrl() . "\n";
    echo "File size: " . $finalResponse->getFileSizeBytes() . " bytes\n";
    echo "Records: " . $finalResponse->getRecordCount() . "\n";
} else {
    echo "Export failed: " . $finalResponse->getErrorMessage() . "\n";
}
```

### Query Task Status

```php
// Query status
$statusRequest = new Export\TaskStatusRequest(['task_id' => $taskId]);
list($statusResponse, $status) = $client->QueryTaskStatus($statusRequest)->wait();

echo "Status: " . $statusResponse->getStatus() . "\n";
echo "Progress: " . $statusResponse->getProgressPercent() . "%\n";

if ($statusResponse->getStatus() === Export\TaskStatus::TASK_STATUS_COMPLETED) {
    echo "Download URL: " . $statusResponse->getOssUrl() . "\n";
}
```

## API Reference

### gRPC Service

#### StreamExport (Streaming RPC)

Streams data for export and returns the result.

**Request Stream**:
- First message: `ExportMetadata` with columns and options
- Subsequent messages: `DataBatch` with records

**Response**:
- `task_id`: Unique task identifier
- `status`: Task status (QUEUED, PROCESSING, UPLOADING, COMPLETED, FAILED)
- `oss_url`: Download URL (when completed)
- `file_size_bytes`: Generated file size
- `record_count`: Total records processed
- `checksum_sha256`: File integrity checksum

#### QueryTaskStatus (Unary RPC)

Queries the current status of an export task.

**Request**:
- `task_id`: Task identifier

**Response**:
- Current task state with progress information
- Download URL when completed
- Error details if failed

## Performance

Based on design targets:

| Metric | Target | Notes |
|--------|--------|-------|
| Memory Usage | <100MB peak | For any dataset size |
| Processing Speed | >10k records/sec (CSV) | Streaming write |
| Processing Speed | >5k records/sec (Excel) | Streaming write |
| Concurrent Tasks | 10+ | Configurable |
| Dataset Size | 500k-800k records | Tested scale |

## Monitoring

### Health Checks

```bash
# Liveness probe
curl http://localhost:8080/health/live

# Readiness probe  
curl http://localhost:8080/health/ready
```

### Metrics

Prometheus metrics available at `http://localhost:8080/metrics`:

- `export_active_tasks` - Current concurrent tasks
- `export_queued_tasks` - Tasks waiting in queue
- `export_duration_seconds` - Export processing time distribution
- `export_upload_duration_seconds` - OSS upload time distribution
- `export_errors_total` - Failure count
- `export_records_processed_total` - Total throughput

### Structured Logs

All operations are logged in JSON format with context propagation:

```json
{
  "timestamp": "2026-01-09T10:30:45.123Z",
  "level": "INFO",
  "task_id": "abc123",
  "component": "task_manager",
  "event": "TaskCompleted",
  "message": "Export task completed successfully",
  "fields": {
    "format": "CSV",
    "records": 500000,
    "file_size": 52428800
  },
  "duration": 45000
}
```

## Development

### Project Structure

```
.
├── cmd/
│   └── server/          # Main service entry point
├── pkg/
│   ├── config/          # Configuration management
│   ├── logger/          # Structured logging
│   ├── taskmanager/     # Task coordination
│   ├── writer/          # Format writers (CSV, Excel)
│   ├── storage/         # Temporary file management
│   ├── oss/             # OSS uploader
│   └── grpc/            # gRPC server implementation
├── proto/               # Protocol buffer definitions
├── tests/
│   ├── unit/            # Unit tests
│   └── integration/     # Integration tests
└── deployments/         # Docker and K8s configs
```

### Running Tests

```bash
# Run all tests
go test ./...

# Run with coverage
go test -cover ./...

# Run integration tests
go test -tags=integration ./tests/integration/...
```

### Building

```bash
# Build for current platform
go build -o bin/export-server cmd/server/main.go

# Build for Linux
GOOS=linux GOARCH=amd64 go build -o bin/export-server-linux cmd/server/main.go

# Build Docker image
docker build -t export-middleware:latest .
```

## Deployment

### Docker

```bash
docker run -d \
  -p 9090:9090 \
  -p 9091:9091 \
  -p 8080:8080 \
  -e OSS_ENDPOINT=oss-cn-hangzhou.aliyuncs.com \
  -e OSS_BUCKET=your-bucket \
  -e OSS_ACCESS_KEY_ID=your-key-id \
  -e OSS_ACCESS_KEY_SECRET=your-secret \
  -v /tmp/export-middleware:/tmp/export-middleware \
  export-middleware:latest
```

### Kubernetes

See `deployments/k8s/` for Kubernetes manifests.

### Systemd

```bash
# Copy binary
sudo cp bin/export-server /usr/local/bin/

# Create systemd service
sudo cp deployments/systemd/export-middleware.service /etc/systemd/system/

# Start service
sudo systemctl enable export-middleware
sudo systemctl start export-middleware
```

## Troubleshooting

### Common Issues

**Q: Export fails with "OSS upload error"**
- Check OSS credentials and bucket permissions
- Verify network connectivity to OSS endpoint
- Check logs for detailed error messages

**Q: High memory usage**
- Reduce `buffer_size` in configuration
- Lower `max_concurrent_tasks`
- Check for memory leaks in logs

**Q: Slow export speed**
- Increase `buffer_size` for better I/O performance
- Increase `parallel_parts` for OSS uploads
- Check disk I/O performance

**Q: Tasks stuck in QUEUED state**
- Check current `active_tasks` count
- Increase `max_concurrent_tasks` if needed
- Check system resources (CPU, memory)

## Contributing

Contributions are welcome! Please read [CONTRIBUTING.md](CONTRIBUTING.md) for details.

## License

[MIT License](LICENSE)

## Support

For issues and questions:
- GitHub Issues: https://github.com/fluxo/export-middleware/issues
- Documentation: https://github.com/fluxo/export-middleware/wiki
