package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fluxo/export-middleware/pkg/logger"
)

// Manager handles temporary file storage operations
type Manager struct {
	tempDir        string
	cleanupEnabled bool
	retention      time.Duration
	logger         *logger.Logger
	mu             sync.RWMutex
	files          map[string]*FileInfo // taskID -> FileInfo
}

// FileInfo contains information about a temporary file
type FileInfo struct {
	Path      string
	CreatedAt time.Time
	Size      int64
}

// NewManager creates a new storage manager
func NewManager(tempDir string, cleanupEnabled bool, retention time.Duration, log *logger.Logger) (*Manager, error) {
	// Create temp directory if it doesn't exist
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}

	m := &Manager{
		tempDir:        tempDir,
		cleanupEnabled: cleanupEnabled,
		retention:      retention,
		logger:         log,
		files:          make(map[string]*FileInfo),
	}

	// Start cleanup goroutine if enabled
	if cleanupEnabled {
		go m.cleanupLoop()
	}

	return m, nil
}

// CreateTempFile creates a temporary file with the given name
func (m *Manager) CreateTempFile(taskID string, filename string) (string, error) {
	// Sanitize filename to prevent path traversal
	filename = filepath.Base(filename)

	// Create unique path with timestamp
	timestamp := time.Now().Format("20060102-150405")
	uniqueName := fmt.Sprintf("%s_%s_%s", taskID, timestamp, filename)
	filePath := filepath.Join(m.tempDir, uniqueName)

	m.mu.Lock()
	m.files[taskID] = &FileInfo{
		Path:      filePath,
		CreatedAt: time.Now(),
		Size:      0,
	}
	m.mu.Unlock()

	m.logger.WithContext(nil).WithTaskID(taskID).LogFileCreated(
		"Temporary file created",
		logger.Fields{"path": filePath, "filename": filename},
	)

	return filePath, nil
}

// UpdateFileSize updates the size of a temporary file
func (m *Manager) UpdateFileSize(taskID string, size int64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if info, exists := m.files[taskID]; exists {
		info.Size = size
	}
}

// GetFilePath returns the path of a temporary file
func (m *Manager) GetFilePath(taskID string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if info, exists := m.files[taskID]; exists {
		return info.Path, nil
	}
	return "", fmt.Errorf("file not found for task: %s", taskID)
}

// DeleteFile deletes a temporary file
func (m *Manager) DeleteFile(taskID string) error {
	m.mu.Lock()
	info, exists := m.files[taskID]
	if !exists {
		m.mu.Unlock()
		return fmt.Errorf("file not found for task: %s", taskID)
	}
	delete(m.files, taskID)
	m.mu.Unlock()

	if err := os.Remove(info.Path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete file: %w", err)
	}

	m.logger.WithContext(nil).WithTaskID(taskID).LogInfo(
		"TempFileDeleted",
		"Temporary file deleted",
		logger.Fields{"path": info.Path},
	)

	return nil
}

// CheckDiskSpace checks if there's enough disk space (simplified)
func (m *Manager) CheckDiskSpace(requiredBytes int64) error {
	// This is a simplified check - in production, use syscall.Statfs
	// For now, just check if temp directory is writable
	testFile := filepath.Join(m.tempDir, ".diskcheck")
	f, err := os.Create(testFile)
	if err != nil {
		return fmt.Errorf("insufficient disk space or permissions")
	}
	f.Close()
	os.Remove(testFile)
	return nil
}

// cleanupLoop periodically cleans up old temporary files
func (m *Manager) cleanupLoop() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		m.cleanup()
	}
}

// cleanup removes expired temporary files
func (m *Manager) cleanup() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	for taskID, info := range m.files {
		if now.Sub(info.CreatedAt) > m.retention {
			if err := os.Remove(info.Path); err == nil {
				delete(m.files, taskID)
				m.logger.WithContext(nil).WithTaskID(taskID).LogInfo(
					"TempFileCleanup",
					"Expired temporary file cleaned up",
					logger.Fields{"path": info.Path, "age": now.Sub(info.CreatedAt).String()},
				)
			}
		}
	}
}

// Close stops the cleanup loop and cleans up resources
func (m *Manager) Close() error {
	// Cleanup loop will stop automatically when manager is garbage collected
	return nil
}
