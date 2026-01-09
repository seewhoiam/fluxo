package logger

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Level represents log severity level
type Level int

const (
	DebugLevel Level = iota
	InfoLevel
	WarnLevel
	ErrorLevel
	FatalLevel
)

// String returns the string representation of the level
func (l Level) String() string {
	switch l {
	case DebugLevel:
		return "DEBUG"
	case InfoLevel:
		return "INFO"
	case WarnLevel:
		return "WARN"
	case ErrorLevel:
		return "ERROR"
	case FatalLevel:
		return "FATAL"
	default:
		return "UNKNOWN"
	}
}

// ParseLevel converts string to Level
func ParseLevel(s string) Level {
	switch strings.ToLower(s) {
	case "debug":
		return DebugLevel
	case "info":
		return InfoLevel
	case "warn", "warning":
		return WarnLevel
	case "error":
		return ErrorLevel
	case "fatal":
		return FatalLevel
	default:
		return InfoLevel
	}
}

// Fields represents additional structured fields for logging
type Fields map[string]interface{}

// Logger provides structured logging with context propagation
type Logger struct {
	level         Level
	output        io.Writer
	formatJSON    bool
	enableTracing bool
	mu            sync.Mutex
}

// LogEntry represents a single log entry
type LogEntry struct {
	Timestamp string                 `json:"timestamp"`
	Level     string                 `json:"level"`
	TaskID    string                 `json:"task_id,omitempty"`
	SessionID string                 `json:"session_id,omitempty"`
	TraceID   string                 `json:"trace_id,omitempty"`
	Component string                 `json:"component,omitempty"`
	Event     string                 `json:"event,omitempty"`
	Message   string                 `json:"message"`
	Fields    map[string]interface{} `json:"fields,omitempty"`
	Duration  int64                  `json:"duration,omitempty"`
	Error     *ErrorInfo             `json:"error,omitempty"`
	Caller    string                 `json:"caller,omitempty"`
}

// ErrorInfo contains detailed error information
type ErrorInfo struct {
	Code       string `json:"code,omitempty"`
	Message    string `json:"message"`
	StackTrace string `json:"stack_trace,omitempty"`
}

// New creates a new Logger instance
func New(level string, format string, output string, enableTracing bool) (*Logger, error) {
	var out io.Writer
	switch output {
	case "stdout":
		out = os.Stdout
	case "stderr":
		out = os.Stderr
	default:
		file, err := os.OpenFile(output, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return nil, fmt.Errorf("failed to open log file: %w", err)
		}
		out = file
	}

	return &Logger{
		level:         ParseLevel(level),
		output:        out,
		formatJSON:    format == "json",
		enableTracing: enableTracing,
	}, nil
}

// WithContext creates a new logger with context values
func (l *Logger) WithContext(ctx context.Context) *ContextLogger {
	return &ContextLogger{
		logger: l,
		ctx:    ctx,
	}
}

// log writes a log entry
func (l *Logger) log(level Level, msg string, fields Fields) {
	if level < l.level {
		return
	}

	entry := LogEntry{
		Timestamp: time.Now().Format(time.RFC3339Nano),
		Level:     level.String(),
		Message:   msg,
		Fields:    fields,
	}

	// Add caller information
	if l.enableTracing {
		if _, file, line, ok := runtime.Caller(3); ok {
			entry.Caller = fmt.Sprintf("%s:%d", file, line)
		}
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.formatJSON {
		data, _ := json.Marshal(entry)
		fmt.Fprintln(l.output, string(data))
	} else {
		fmt.Fprintf(l.output, "[%s] %s %s\n", entry.Timestamp, entry.Level, entry.Message)
	}
}

// Debug logs a debug message
func (l *Logger) Debug(msg string, fields ...Fields) {
	l.log(DebugLevel, msg, mergeFields(fields...))
}

// Info logs an info message
func (l *Logger) Info(msg string, fields ...Fields) {
	l.log(InfoLevel, msg, mergeFields(fields...))
}

// Warn logs a warning message
func (l *Logger) Warn(msg string, fields ...Fields) {
	l.log(WarnLevel, msg, mergeFields(fields...))
}

// Error logs an error message
func (l *Logger) Error(msg string, fields ...Fields) {
	l.log(ErrorLevel, msg, mergeFields(fields...))
}

// Fatal logs a fatal message and exits
func (l *Logger) Fatal(msg string, fields ...Fields) {
	l.log(FatalLevel, msg, mergeFields(fields...))
	os.Exit(1)
}

// ContextLogger wraps Logger with context information
type ContextLogger struct {
	logger    *Logger
	ctx       context.Context
	taskID    string
	sessionID string
	traceID   string
	component string
}

// WithTaskID adds task ID to the context logger
func (cl *ContextLogger) WithTaskID(taskID string) *ContextLogger {
	cl.taskID = taskID
	return cl
}

// WithSessionID adds session ID to the context logger
func (cl *ContextLogger) WithSessionID(sessionID string) *ContextLogger {
	cl.sessionID = sessionID
	return cl
}

// WithTraceID adds trace ID to the context logger
func (cl *ContextLogger) WithTraceID(traceID string) *ContextLogger {
	cl.traceID = traceID
	return cl
}

// WithComponent adds component name to the context logger
func (cl *ContextLogger) WithComponent(component string) *ContextLogger {
	cl.component = component
	return cl
}

// log writes a contextualized log entry
func (cl *ContextLogger) log(level Level, event string, msg string, fields Fields, duration int64, err *ErrorInfo) {
	if level < cl.logger.level {
		return
	}

	entry := LogEntry{
		Timestamp: time.Now().Format(time.RFC3339Nano),
		Level:     level.String(),
		TaskID:    cl.taskID,
		SessionID: cl.sessionID,
		TraceID:   cl.traceID,
		Component: cl.component,
		Event:     event,
		Message:   msg,
		Fields:    fields,
		Duration:  duration,
		Error:     err,
	}

	// Add caller information
	if cl.logger.enableTracing {
		if _, file, line, ok := runtime.Caller(2); ok {
			entry.Caller = fmt.Sprintf("%s:%d", file, line)
		}
	}

	cl.logger.mu.Lock()
	defer cl.logger.mu.Unlock()

	if cl.logger.formatJSON {
		data, _ := json.Marshal(entry)
		fmt.Fprintln(cl.logger.output, string(data))
	} else {
		msg := fmt.Sprintf("[%s] %s [%s] %s", entry.Timestamp, entry.Level, event, entry.Message)
		if cl.taskID != "" {
			msg = fmt.Sprintf("%s taskID=%s", msg, cl.taskID)
		}
		fmt.Fprintln(cl.logger.output, msg)
	}
}

// LogTaskCreated logs task creation
func (cl *ContextLogger) LogTaskCreated(msg string, fields Fields) {
	cl.log(InfoLevel, "TaskCreated", msg, fields, 0, nil)
}

// LogTaskCompleted logs task completion
func (cl *ContextLogger) LogTaskCompleted(msg string, duration int64, fields Fields) {
	cl.log(InfoLevel, "TaskCompleted", msg, fields, duration, nil)
}

// LogTaskFailed logs task failure
func (cl *ContextLogger) LogTaskFailed(msg string, errorCode string, errorMsg string, fields Fields) {
	cl.log(ErrorLevel, "TaskFailed", msg, fields, 0, &ErrorInfo{
		Code:    errorCode,
		Message: errorMsg,
	})
}

// LogBatchProcessed logs batch processing
func (cl *ContextLogger) LogBatchProcessed(msg string, duration int64, fields Fields) {
	cl.log(DebugLevel, "BatchProcessed", msg, fields, duration, nil)
}

// LogFileCreated logs file creation
func (cl *ContextLogger) LogFileCreated(msg string, fields Fields) {
	cl.log(InfoLevel, "FileCreated", msg, fields, 0, nil)
}

// LogFileFinalized logs file finalization
func (cl *ContextLogger) LogFileFinalized(msg string, duration int64, fields Fields) {
	cl.log(InfoLevel, "FileFinalized", msg, fields, duration, nil)
}

// LogOSSUploadStarted logs OSS upload start
func (cl *ContextLogger) LogOSSUploadStarted(msg string, fields Fields) {
	cl.log(InfoLevel, "OSSUploadStarted", msg, fields, 0, nil)
}

// LogOSSUploadCompleted logs OSS upload completion
func (cl *ContextLogger) LogOSSUploadCompleted(msg string, duration int64, fields Fields) {
	cl.log(InfoLevel, "OSSUploadCompleted", msg, fields, duration, nil)
}

// LogOSSUploadFailed logs OSS upload failure
func (cl *ContextLogger) LogOSSUploadFailed(msg string, errorCode string, errorMsg string, fields Fields) {
	cl.log(ErrorLevel, "OSSUploadFailed", msg, fields, 0, &ErrorInfo{
		Code:    errorCode,
		Message: errorMsg,
	})
}

// LogError logs a generic error
func (cl *ContextLogger) LogError(event string, msg string, errorCode string, errorMsg string, fields Fields) {
	cl.log(ErrorLevel, event, msg, fields, 0, &ErrorInfo{
		Code:    errorCode,
		Message: errorMsg,
	})
}

// LogInfo logs a generic info message
func (cl *ContextLogger) LogInfo(event string, msg string, fields Fields) {
	cl.log(InfoLevel, event, msg, fields, 0, nil)
}

// LogDebug logs a generic debug message
func (cl *ContextLogger) LogDebug(event string, msg string, fields Fields) {
	cl.log(DebugLevel, event, msg, fields, 0, nil)
}

// LogWarn logs a generic warning message
func (cl *ContextLogger) LogWarn(event string, msg string, fields Fields) {
	cl.log(WarnLevel, event, msg, fields, 0, nil)
}

// mergeFields merges multiple Fields into one
func mergeFields(fields ...Fields) Fields {
	result := Fields{}
	for _, f := range fields {
		for k, v := range f {
			result[k] = v
		}
	}
	return result
}
