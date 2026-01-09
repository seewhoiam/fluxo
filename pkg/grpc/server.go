package grpcserver

import (
	"context"
	"fmt"
	"io"
	"net"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	grpcStatus "google.golang.org/grpc/status"

	"github.com/fluxo/export-middleware/pkg/config"
	"github.com/fluxo/export-middleware/pkg/logger"
	"github.com/fluxo/export-middleware/pkg/taskmanager"
	pb "github.com/fluxo/export-middleware/proto"
)

// Server implements the ExportService gRPC server
type Server struct {
	pb.UnimplementedExportServiceServer
	config      *config.Config
	logger      *logger.Logger
	taskManager *taskmanager.Manager
	grpcServer  *grpc.Server
}

// NewServer creates a new gRPC server
func NewServer(cfg *config.Config, log *logger.Logger, taskMgr *taskmanager.Manager) *Server {
	return &Server{
		config:      cfg,
		logger:      log,
		taskManager: taskMgr,
	}
}

// Start starts the gRPC server
func (s *Server) Start() error {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", s.config.Server.Port))
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}

	s.grpcServer = grpc.NewServer(
		grpc.MaxRecvMsgSize(int(s.config.Performance.BufferSize)),
		grpc.MaxSendMsgSize(int(s.config.Performance.BufferSize)),
		grpc.ConnectionTimeout(s.config.Server.Timeout),
	)

	pb.RegisterExportServiceServer(s.grpcServer, s)

	s.logger.Info("gRPC server starting", logger.Fields{"port": s.config.Server.Port})

	go func() {
		if err := s.grpcServer.Serve(lis); err != nil {
			s.logger.Error("gRPC server error", logger.Fields{"error": err.Error()})
		}
	}()

	return nil
}

// Stop gracefully stops the gRPC server
func (s *Server) Stop() {
	if s.grpcServer != nil {
		s.logger.Info("Stopping gRPC server...")
		s.grpcServer.GracefulStop()
		s.logger.Info("gRPC server stopped")
	}
}

// StreamExport handles streaming export requests
func (s *Server) StreamExport(stream pb.ExportService_StreamExportServer) error {
	ctx := stream.Context()
	contextLogger := s.logger.WithContext(ctx).WithComponent("grpc_server")

	// Receive first message (metadata)
	firstMsg, err := stream.Recv()
	if err != nil {
		contextLogger.LogError("StreamReceiveError", "Failed to receive first message", "STREAM_ERROR", err.Error(), nil)
		return grpcStatus.Error(codes.InvalidArgument, "failed to receive metadata")
	}

	metadata := firstMsg.GetMetadata()
	if metadata == nil {
		contextLogger.LogError("ValidationError", "First message must contain metadata", "INVALID_METADATA", "metadata is nil", nil)
		return grpcStatus.Error(codes.InvalidArgument, "first message must contain metadata")
	}

	// Validate metadata
	if err := s.validateMetadata(metadata); err != nil {
		contextLogger.LogError("ValidationError", "Invalid metadata", "VALIDATION_ERROR", err.Error(), nil)
		return grpcStatus.Error(codes.InvalidArgument, err.Error())
	}

	// Create task
	task, err := s.taskManager.CreateTask(ctx, metadata)
	if err != nil {
		contextLogger.LogError("TaskCreationError", "Failed to create task", "TASK_ERROR", err.Error(), nil)
		return grpcStatus.Error(codes.ResourceExhausted, "failed to create task")
	}

	taskLogger := contextLogger.WithTaskID(task.ID)
	taskLogger.LogInfo("StreamStarted", "Export stream started", logger.Fields{"format": metadata.Format.String()})

	// Send task ID back to client immediately
	response := &pb.ExportResponse{
		TaskId: task.ID,
		Status: pb.TaskStatus_TASK_STATUS_QUEUED,
	}
	// Note: In streaming RPC, we can't send response immediately
	// Client needs to track the task ID from initial metadata or wait for completion

	// Write headers
	if err := task.Writer.WriteHeader(metadata.Columns); err != nil {
		s.taskManager.GetTask(task.ID) // Get task for cleanup
		taskLogger.LogError("WriteHeaderError", "Failed to write headers", "WRITER_ERROR", err.Error(), nil)
		return grpcStatus.Error(codes.Internal, "failed to write headers")
	}

	// Process data batches
	batchCount := int64(0)
	recordCount := int64(0)
	startTime := time.Now()

	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			// End of stream
			break
		}
		if err != nil {
			taskLogger.LogError("StreamError", "Stream receive error", "STREAM_ERROR", err.Error(), nil)
			if task.Writer != nil {
				task.Writer.Cleanup()
			}
			return grpcStatus.Error(codes.Internal, "stream error")
		}

		batch := msg.GetBatch()
		if batch == nil {
			continue
		}

		// Write records
		batchStartTime := time.Now()
		if err := task.Writer.WriteRecords(batch.Records); err != nil {
			taskLogger.LogError("WriteError", "Failed to write records", "WRITER_ERROR", err.Error(), logger.Fields{
				"batch_sequence": batch.BatchSequence,
			})
			if task.Writer != nil {
				task.Writer.Cleanup()
			}
			return grpcStatus.Error(codes.Internal, "failed to write records")
		}

		batchCount++
		recordCount += int64(len(batch.Records))
		batchDuration := time.Since(batchStartTime)

		// Update progress
		if metadata.Format == pb.ExportFormat_FORMAT_CSV {
			// For CSV, we can estimate progress
			progress := float32(recordCount) / float32(recordCount+1000) * 100 // Rough estimate
			if progress > 100 {
				progress = 99 // Cap at 99 until finalization
			}
			s.taskManager.UpdateTaskProgress(task.ID, recordCount, progress)
		}

		taskLogger.LogBatchProcessed(
			fmt.Sprintf("Batch %d processed", batch.BatchSequence),
			batchDuration.Milliseconds(),
			logger.Fields{
				"batch_sequence": batch.BatchSequence,
				"records":        len(batch.Records),
				"total_records":  recordCount,
			},
		)
	}

	taskLogger.LogInfo("StreamCompleted", "All batches received", logger.Fields{
		"batch_count":  batchCount,
		"record_count": recordCount,
		"duration_ms":  time.Since(startTime).Milliseconds(),
	})

	// Finalize task
	if err := s.taskManager.FinalizeTask(task); err != nil {
		taskLogger.LogError("FinalizeError", "Failed to finalize task", "FINALIZE_ERROR", err.Error(), nil)
		return grpcStatus.Error(codes.Internal, "failed to finalize export")
	}

	// Get final task status
	finalStatus, err := s.taskManager.GetTaskStatus(task.ID)
	if err != nil {
		return grpcStatus.Error(codes.Internal, "failed to get task status")
	}

	// Send final response
	response = &pb.ExportResponse{
		TaskId:          finalStatus.TaskId,
		Status:          finalStatus.Status,
		OssUrl:          finalStatus.OssUrl,
		FileSizeBytes:   finalStatus.FileSizeBytes,
		RecordCount:     finalStatus.RecordsProcessed,
		ProgressPercent: 100,
		StartTime:       finalStatus.StartTime,
		CompletionTime:  finalStatus.CompletionTime,
	}

	taskLogger.LogInfo("ExportCompleted", "Export completed successfully", logger.Fields{
		"oss_url":    response.OssUrl,
		"file_size":  response.FileSizeBytes,
		"records":    response.RecordCount,
		"duration_s": time.Since(startTime).Seconds(),
	})

	return stream.SendAndClose(response)
}

// QueryTaskStatus handles task status queries
func (s *Server) QueryTaskStatus(ctx context.Context, req *pb.TaskStatusRequest) (*pb.TaskStatusResponse, error) {
	contextLogger := s.logger.WithContext(ctx).WithComponent("grpc_server").WithTaskID(req.TaskId)

	contextLogger.LogInfo("StatusQueried", "Task status query received", nil)

	status, err := s.taskManager.GetTaskStatus(req.TaskId)
	if err != nil {
		contextLogger.LogWarn("StatusNotFound", "Task not found", logger.Fields{"error": err.Error()})
		return nil, grpcStatus.Error(codes.NotFound, "task not found")
	}

	return status, nil
}

// validateMetadata validates export metadata
func (s *Server) validateMetadata(metadata *pb.ExportMetadata) error {
	if metadata.RequestId == "" {
		return fmt.Errorf("request_id is required")
	}
	if metadata.Format == pb.ExportFormat_FORMAT_UNSPECIFIED {
		return fmt.Errorf("format must be specified")
	}
	if metadata.Filename == "" {
		return fmt.Errorf("filename is required")
	}
	if len(metadata.Columns) == 0 {
		return fmt.Errorf("at least one column is required")
	}

	// Validate columns
	for i, col := range metadata.Columns {
		if col.Name == "" {
			return fmt.Errorf("column %d name is required", i)
		}
		if col.DataType == pb.DataType_DATA_TYPE_UNSPECIFIED {
			return fmt.Errorf("column %d data type is required", i)
		}
	}

	return nil
}
