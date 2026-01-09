package oss

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/aliyun/aliyun-oss-go-sdk/oss"
	"github.com/fluxo/export-middleware/pkg/config"
	"github.com/fluxo/export-middleware/pkg/logger"
)

// Uploader handles file uploads to Alibaba Cloud OSS
type Uploader struct {
	client *oss.Client
	bucket *oss.Bucket
	config *config.OSSConfig
	logger *logger.Logger
}

// UploadResult contains the result of an upload operation
type UploadResult struct {
	ObjectKey  string
	SignedURL  string
	Size       int64
	UploadTime time.Duration
}

// NewUploader creates a new OSS uploader
func NewUploader(cfg *config.OSSConfig, log *logger.Logger) (*Uploader, error) {
	// Create OSS client
	client, err := oss.New(cfg.Endpoint, cfg.AccessKeyID, cfg.AccessKeySecret)
	if err != nil {
		return nil, fmt.Errorf("failed to create OSS client: %w", err)
	}

	// Get bucket
	bucket, err := client.Bucket(cfg.Bucket)
	if err != nil {
		return nil, fmt.Errorf("failed to get OSS bucket: %w", err)
	}

	return &Uploader{
		client: client,
		bucket: bucket,
		config: cfg,
		logger: log,
	}, nil
}

// Upload uploads a file to OSS with retry logic
func (u *Uploader) Upload(ctx context.Context, taskID string, localPath string) (*UploadResult, error) {
	startTime := time.Now()

	// Get file info
	fileInfo, err := os.Stat(localPath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}

	// Generate object key (path in OSS)
	objectKey := u.generateObjectKey(localPath)

	contextLogger := u.logger.WithContext(ctx).WithTaskID(taskID).WithComponent("oss_uploader")
	contextLogger.LogOSSUploadStarted(
		"Starting OSS upload",
		logger.Fields{
			"object_key": objectKey,
			"file_size":  fileInfo.Size(),
			"local_path": localPath,
		},
	)

	var lastErr error
	for attempt := 0; attempt <= u.config.MaxRetries; attempt++ {
		if attempt > 0 {
			waitTime := time.Duration(attempt) * time.Second
			contextLogger.LogWarn(
				"OSSUploadRetry",
				fmt.Sprintf("Retrying upload (attempt %d/%d)", attempt+1, u.config.MaxRetries+1),
				logger.Fields{"wait_time": waitTime.String()},
			)
			time.Sleep(waitTime)
		}

		// Choose upload strategy based on file size
		if fileInfo.Size() > u.config.PartSize {
			lastErr = u.multiPartUpload(ctx, taskID, localPath, objectKey, contextLogger)
		} else {
			lastErr = u.simpleUpload(ctx, localPath, objectKey)
		}

		if lastErr == nil {
			break
		}
	}

	if lastErr != nil {
		contextLogger.LogOSSUploadFailed(
			"OSS upload failed after retries",
			"UPLOAD_ERROR",
			lastErr.Error(),
			logger.Fields{
				"object_key": objectKey,
				"attempts":   u.config.MaxRetries + 1,
			},
		)
		return nil, fmt.Errorf("failed to upload after %d attempts: %w", u.config.MaxRetries+1, lastErr)
	}

	// Generate signed URL
	signedURL, err := u.generateSignedURL(objectKey)
	if err != nil {
		return nil, fmt.Errorf("failed to generate signed URL: %w", err)
	}

	duration := time.Since(startTime)
	contextLogger.LogOSSUploadCompleted(
		"OSS upload completed successfully",
		duration.Milliseconds(),
		logger.Fields{
			"object_key": objectKey,
			"signed_url": signedURL,
			"file_size":  fileInfo.Size(),
		},
	)

	return &UploadResult{
		ObjectKey:  objectKey,
		SignedURL:  signedURL,
		Size:       fileInfo.Size(),
		UploadTime: duration,
	}, nil
}

// simpleUpload uploads a file in a single request
func (u *Uploader) simpleUpload(ctx context.Context, localPath string, objectKey string) error {
	return u.bucket.PutObjectFromFile(objectKey, localPath)
}

// multiPartUpload uploads a file using multi-part upload
func (u *Uploader) multiPartUpload(ctx context.Context, taskID string, localPath string, objectKey string, contextLogger *logger.ContextLogger) error {
	// Initialize multi-part upload
	imur, err := u.bucket.InitiateMultipartUpload(objectKey)
	if err != nil {
		return fmt.Errorf("failed to initiate multi-part upload: %w", err)
	}

	// Open file
	file, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	// Get file size
	fileInfo, err := file.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat file: %w", err)
	}

	// Calculate part count
	partSize := u.config.PartSize
	partCount := int(fileInfo.Size() / partSize)
	if fileInfo.Size()%partSize != 0 {
		partCount++
	}

	// Upload parts
	var parts []oss.UploadPart
	for partNum := 1; partNum <= partCount; partNum++ {
		offset := int64(partNum-1) * partSize
		size := partSize
		if offset+size > fileInfo.Size() {
			size = fileInfo.Size() - offset
		}

		part, err := u.bucket.UploadPartFromFile(imur, localPath, offset, size, partNum)
		if err != nil {
			// Abort multi-part upload on error
			u.bucket.AbortMultipartUpload(imur)
			return fmt.Errorf("failed to upload part %d: %w", partNum, err)
		}

		parts = append(parts, part)

		contextLogger.LogDebug(
			"OSSPartUploaded",
			fmt.Sprintf("Uploaded part %d/%d", partNum, partCount),
			logger.Fields{
				"part_number": partNum,
				"part_size":   size,
			},
		)
	}

	// Complete multi-part upload
	_, err = u.bucket.CompleteMultipartUpload(imur, parts)
	if err != nil {
		u.bucket.AbortMultipartUpload(imur)
		return fmt.Errorf("failed to complete multi-part upload: %w", err)
	}

	return nil
}

// generateObjectKey creates an object key from local path
func (u *Uploader) generateObjectKey(localPath string) string {
	// Use filename with date prefix
	filename := filepath.Base(localPath)
	datePrefix := time.Now().Format("2006/01/02")
	return fmt.Sprintf("exports/%s/%s", datePrefix, filename)
}

// generateSignedURL creates a signed URL for downloading
func (u *Uploader) generateSignedURL(objectKey string) (string, error) {
	expiry := int64(u.config.SignedURLExpiry.Seconds())
	signedURL, err := u.bucket.SignURL(objectKey, oss.HTTPGet, expiry)
	if err != nil {
		return "", fmt.Errorf("failed to sign URL: %w", err)
	}
	return signedURL, nil
}

// DeleteObject deletes an object from OSS
func (u *Uploader) DeleteObject(objectKey string) error {
	return u.bucket.DeleteObject(objectKey)
}

// Close cleans up resources
func (u *Uploader) Close() error {
	// OSS client doesn't need explicit cleanup
	return nil
}
