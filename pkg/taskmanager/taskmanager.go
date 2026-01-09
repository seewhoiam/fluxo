package taskmanager

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/fluxo/export-middleware/pkg/config"
	"github.com/fluxo/export-middleware/pkg/logger"
	"github.com/fluxo/export-middleware/pkg/oss"
	"github.com/fluxo/export-middleware/pkg/storage"
	"github.com/fluxo/export-middleware/pkg/writer"
	pb "github.com/fluxo/export-middleware/proto"
	"github.com/google/uuid"
)

// TaskStatus represents the current state of a task
type TaskStatus int

const (
	StatusQueued TaskStatus = iota
	StatusProcessing
	StatusUploading
	StatusCompleted
	StatusFailed
)

// Task represents an export task
type Task struct {
	ID               string
	Status           TaskStatus
	Format           pb.ExportFormat
	Filename         string
	Metadata         *pb.ExportMetadata
	RecordsProcessed int64
	ProgressPercent  float32
	OSSUrl           string
	FileSizeBytes    int64
	ErrorMessage     string
	ErrorCode        string
	StartTime        time.Time
	CompletionTime   time.Time
	Writer           writer.Writer
	LocalPath        string
	mu               sync.RWMutex
}

// Manager coordinates export tasks with concurrency control
type Manager struct {
	config         *config.Config
	logger         *logger.Logger
	storage        *storage.Manager
	ossUploader    *oss.Uploader
	tasks          map[string]*Task
	taskQueue      chan *Task
	activeTasks    int
	maxConcurrent  int
	mu             sync.RWMutex
	shutdownCtx    context.Context
	shutdownCancel context.CancelFunc
	wg             sync.WaitGroup
}

// NewManager creates a new task manager
func NewManager(cfg *config.Config, log *logger.Logger, storageMgr *storage.Manager, ossUploader *oss.Uploader) *Manager {
	ctx, cancel := context.WithCancel(context.Background())

	m := &Manager{
		config:         cfg,
		logger:         log,
		storage:        storageMgr,
		ossUploader:    ossUploader,
		tasks:          make(map[string]*Task),
		taskQueue:      make(chan *Task, cfg.Concurrency.TaskQueueSize),
		maxConcurrent:  cfg.Concurrency.MaxConcurrentTasks,
		shutdownCtx:    ctx,
		shutdownCancel: cancel,
	}

	// Start worker pool
	for i := 0; i < m.maxConcurrent; i++ {
		m.wg.Add(1)
		go m.worker(i)
	}

	return m
}

// CreateTask creates a new export task
func (m *Manager) CreateTask(ctx context.Context, metadata *pb.ExportMetadata) (*Task, error) {
	taskID := uuid.New().String()

	task := &Task{
		ID:        taskID,
		Status:    StatusQueued,
		Format:    metadata.Format,
		Filename:  metadata.Filename,
		Metadata:  metadata,
		StartTime: time.Now(),
	}

	m.mu.Lock()
	m.tasks[taskID] = task
	m.mu.Unlock()

	contextLogger := m.logger.WithContext(ctx).WithTaskID(taskID).WithComponent("task_manager")
	contextLogger.LogTaskCreated(
		"Export task created",
		logger.Fields{
			"format":   metadata.Format.String(),
			"filename": metadata.Filename,
		},
	)

	// Try to enqueue task
	select {
	case m.taskQueue <- task:
		contextLogger.LogInfo("TaskQueued", "Task queued for processing", logger.Fields{"queue_size": len(m.taskQueue)})
	case <-time.After(m.config.Concurrency.QueueTimeout):
		task.mu.Lock()
		task.Status = StatusFailed
		task.ErrorCode = "QUEUE_TIMEOUT"
		task.ErrorMessage = "Task queue is full, timeout waiting for slot"
		task.mu.Unlock()
		contextLogger.LogWarn("TaskQueueFull", "Task queue timeout", logger.Fields{"timeout": m.config.Concurrency.QueueTimeout})
		return nil, fmt.Errorf("task queue is full")
	}

	return task, nil
}

// GetTaskStatus retrieves the status of a task
func (m *Manager) GetTaskStatus(taskID string) (*pb.TaskStatusResponse, error) {
	m.mu.RLock()
	task, exists := m.tasks[taskID]
	m.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("task not found: %s", taskID)
	}

	task.mu.RLock()
	defer task.mu.RUnlock()

	status := &pb.TaskStatusResponse{
		TaskId:           task.ID,
		Status:           m.convertStatus(task.Status),
		Format:           task.Format,
		Filename:         task.Filename,
		RecordsProcessed: task.RecordsProcessed,
		ProgressPercent:  task.ProgressPercent,
		OssUrl:           task.OSSUrl,
		FileSizeBytes:    task.FileSizeBytes,
		ErrorMessage:     task.ErrorMessage,
		ErrorCode:        task.ErrorCode,
		StartTime:        task.StartTime.Unix(),
	}

	if !task.CompletionTime.IsZero() {
		status.CompletionTime = task.CompletionTime.Unix()
	}

	// Estimate time remaining if processing
	if task.Status == StatusProcessing && task.RecordsProcessed > 0 {
		elapsed := time.Since(task.StartTime).Seconds()
		recordsPerSecond := float64(task.RecordsProcessed) / elapsed
		if recordsPerSecond > 0 && task.ProgressPercent < 100 {
			remainingRecords := float64(task.RecordsProcessed) * (100/float64(task.ProgressPercent) - 1)
			status.EstimatedTimeRemaining = int64(remainingRecords / recordsPerSecond)
		}
	}

	return status, nil
}

// worker processes tasks from the queue
func (m *Manager) worker(id int) {
	defer m.wg.Done()

	for {
		select {
		case <-m.shutdownCtx.Done():
			return
		case task := <-m.taskQueue:
			m.processTask(task)
		}
	}
}

// processTask processes a single export task
func (m *Manager) processTask(task *Task) {
	ctx := context.Background()
	contextLogger := m.logger.WithContext(ctx).WithTaskID(task.ID).WithComponent("task_manager")

	// Update status to processing
	task.mu.Lock()
	task.Status = StatusProcessing
	task.mu.Unlock()

	m.mu.Lock()
	m.activeTasks++
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		m.activeTasks--
		m.mu.Unlock()
	}()

	contextLogger.LogInfo("TaskStarted", "Task processing started", nil)

	// Create temporary file
	localPath, err := m.storage.CreateTempFile(task.ID, task.Filename)
	if err != nil {
		m.failTask(task, "STORAGE_ERROR", fmt.Sprintf("Failed to create temp file: %v", err), contextLogger)
		return
	}
	task.mu.Lock()
	task.LocalPath = localPath
	task.mu.Unlock()

	// Initialize writer based on format
	var w writer.Writer
	switch task.Format {
	case pb.ExportFormat_FORMAT_CSV:
		w = writer.NewCSVWriter()
	case pb.ExportFormat_FORMAT_EXCEL:
		w = writer.NewExcelWriter()
	default:
		m.failTask(task, "INVALID_FORMAT", "Unsupported export format", contextLogger)
		return
	}

	if err := w.Initialize(ctx, task.Metadata, localPath); err != nil {
		m.failTask(task, "WRITER_INIT_ERROR", fmt.Sprintf("Failed to initialize writer: %v", err), contextLogger)
		return
	}

	task.mu.Lock()
	task.Writer = w
	task.mu.Unlock()

	contextLogger.LogInfo("WriterInitialized", "Format writer initialized", logger.Fields{"format": task.Format.String()})
}

// UpdateTaskProgress updates the progress of a task
func (m *Manager) UpdateTaskProgress(taskID string, recordsProcessed int64, progressPercent float32) {
	m.mu.RLock()
	task, exists := m.tasks[taskID]
	m.mu.RUnlock()

	if !exists {
		return
	}

	task.mu.Lock()
	task.RecordsProcessed = recordsProcessed
	task.ProgressPercent = progressPercent
	task.mu.Unlock()
}

// FinalizeTask finalizes the file and uploads to OSS
func (m *Manager) FinalizeTask(task *Task) error {
	ctx := context.Background()
	contextLogger := m.logger.WithContext(ctx).WithTaskID(task.ID).WithComponent("task_manager")

	// Finalize writer
	metadata, err := task.Writer.Finalize()
	if err != nil {
		m.failTask(task, "FINALIZE_ERROR", fmt.Sprintf("Failed to finalize file: %v", err), contextLogger)
		return err
	}

	contextLogger.LogFileFinalized(
		"File finalized successfully",
		time.Since(task.StartTime).Milliseconds(),
		logger.Fields{
			"file_size": metadata.Size,
			"checksum":  metadata.Checksum,
			"rows":      metadata.RowCount,
		},
	)

	// Update task
	task.mu.Lock()
	task.Status = StatusUploading
	task.FileSizeBytes = metadata.Size
	task.RecordsProcessed = metadata.RowCount
	task.mu.Unlock()

	// Upload to OSS
	result, err := m.ossUploader.Upload(ctx, task.ID, metadata.Path)
	if err != nil {
		m.failTask(task, "UPLOAD_ERROR", fmt.Sprintf("Failed to upload to OSS: %v", err), contextLogger)
		return err
	}

	// Update task as completed
	task.mu.Lock()
	task.Status = StatusCompleted
	task.OSSUrl = result.SignedURL
	task.CompletionTime = time.Now()
	task.mu.Unlock()

	duration := time.Since(task.StartTime)
	contextLogger.LogTaskCompleted(
		"Export task completed successfully",
		duration.Milliseconds(),
		logger.Fields{
			"oss_url":     result.SignedURL,
			"file_size":   result.Size,
			"records":     metadata.RowCount,
			"duration_ms": duration.Milliseconds(),
		},
	)

	// Cleanup temp file
	if err := m.storage.DeleteFile(task.ID); err != nil {
		contextLogger.LogWarn("TempFileCleanupError", "Failed to cleanup temp file", logger.Fields{"error": err.Error()})
	}

	return nil
}

// failTask marks a task as failed
func (m *Manager) failTask(task *Task, errorCode string, errorMsg string, contextLogger *logger.ContextLogger) {
	task.mu.Lock()
	task.Status = StatusFailed
	task.ErrorCode = errorCode
	task.ErrorMessage = errorMsg
	task.CompletionTime = time.Now()
	task.mu.Unlock()

	contextLogger.LogTaskFailed(
		"Export task failed",
		errorCode,
		errorMsg,
		nil,
	)

	// Cleanup
	if task.Writer != nil {
		task.Writer.Cleanup()
	}
	if task.LocalPath != "" {
		m.storage.DeleteFile(task.ID)
	}
}

// convertStatus converts internal status to proto status
func (m *Manager) convertStatus(status TaskStatus) pb.TaskStatus {
	switch status {
	case StatusQueued:
		return pb.TaskStatus_TASK_STATUS_QUEUED
	case StatusProcessing:
		return pb.TaskStatus_TASK_STATUS_PROCESSING
	case StatusUploading:
		return pb.TaskStatus_TASK_STATUS_UPLOADING
	case StatusCompleted:
		return pb.TaskStatus_TASK_STATUS_COMPLETED
	case StatusFailed:
		return pb.TaskStatus_TASK_STATUS_FAILED
	default:
		return pb.TaskStatus_TASK_STATUS_UNSPECIFIED
	}
}

// Shutdown gracefully shuts down the task manager
func (m *Manager) Shutdown(ctx context.Context) error {
	m.logger.Info("Shutting down task manager...")
	m.shutdownCancel()

	// Wait for workers to finish with timeout
	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		m.logger.Info("Task manager shutdown complete")
		return nil
	case <-ctx.Done():
		return fmt.Errorf("shutdown timeout")
	}
}

// GetTask retrieves a task by ID
func (m *Manager) GetTask(taskID string) (*Task, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	task, exists := m.tasks[taskID]
	if !exists {
		return nil, fmt.Errorf("task not found: %s", taskID)
	}
	return task, nil
}
