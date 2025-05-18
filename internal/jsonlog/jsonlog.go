package jsonlog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"sync"
	"time"
)

// RequestIDKey is the context key for the request ID
type contextKey string

const RequestIDKey contextKey = "requestID"

// Level represents the severity level of a log entry
type Level int8

const (
	LevelDebug Level = iota - 1 // Added Debug level (lower than Info)
	LevelInfo
	LevelWarn  // Added Warning level
	LevelError
	LevelFatal
	LevelOff
)

func (l Level) String() string {
	switch l {
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	case LevelFatal:
		return "FATAL"
	default:
		return ""
	}
}

// LogConfig holds configuration for the logger
type LogConfig struct {
	// LogPath is the directory where log files will be stored
	LogPath string
	// MaxSize is the maximum size in megabytes of the log file before it gets rotated
	MaxSize int
	// MaxBackups is the maximum number of old log files to retain
	MaxBackups int
	// MaxAge is the maximum number of days to retain old log files
	MaxAge int
	// Compress determines if the rotated log files should be compressed
	Compress bool
}

// Logger is a JSON-formatted logger with support for log levels and context
type Logger struct {
	out         io.Writer
	minLevel    Level
	mu          sync.Mutex
	config      *LogConfig
	currentFile *os.File
	fileSize    int64
}

// New creates a new Logger that writes to the specified writer
func New(out io.Writer, minLevel Level) *Logger {
	return &Logger{
		out:      out,
		minLevel: minLevel,
	}
}

// NewFileLogger creates a new Logger that writes to a file with rotation support
func NewFileLogger(config LogConfig, minLevel Level) (*Logger, error) {
	// Create log directory if it doesn't exist
	if err := os.MkdirAll(config.LogPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create log directory: %w", err)
	}

	// Create initial log file
	logFilePath := filepath.Join(config.LogPath, fmt.Sprintf("app-%s.log", time.Now().Format("2006-01-02")))
	file, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file: %w", err)
	}

	// Get initial file size
	fileInfo, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("failed to get file info: %w", err)
	}

	return &Logger{
		out:         file,
		minLevel:    minLevel,
		config:      &config,
		currentFile: file,
		fileSize:    fileInfo.Size(),
	}, nil
}

// PrintDebug logs a message at DEBUG level
func (l *Logger) PrintDebug(message string, properties map[string]string) {
	l.print(LevelDebug, message, properties)
}

// PrintInfo logs a message at INFO level
func (l *Logger) PrintInfo(message string, properties map[string]string) {
	l.print(LevelInfo, message, properties)
}

// PrintWarn logs a message at WARN level
func (l *Logger) PrintWarn(message string, properties map[string]string) {
	l.print(LevelWarn, message, properties)
}

// PrintError logs an error at ERROR level
func (l *Logger) PrintError(err error, properties map[string]string) {
	l.print(LevelError, err.Error(), properties)
}

// PrintFatal logs an error at FATAL level and terminates the application
func (l *Logger) PrintFatal(err error, properties map[string]string) {
	l.print(LevelFatal, err.Error(), properties)
	os.Exit(1) // For entries at the FATAL level, we also terminate the application.
}

// PrintDebugWithContext logs a message at DEBUG level with context
func (l *Logger) PrintDebugWithContext(ctx context.Context, message string, properties map[string]string) {
	l.printWithContext(ctx, LevelDebug, message, properties)
}

// PrintInfoWithContext logs a message at INFO level with context
func (l *Logger) PrintInfoWithContext(ctx context.Context, message string, properties map[string]string) {
	l.printWithContext(ctx, LevelInfo, message, properties)
}

// PrintWarnWithContext logs a message at WARN level with context
func (l *Logger) PrintWarnWithContext(ctx context.Context, message string, properties map[string]string) {
	l.printWithContext(ctx, LevelWarn, message, properties)
}

// PrintErrorWithContext logs an error at ERROR level with context
func (l *Logger) PrintErrorWithContext(ctx context.Context, err error, properties map[string]string) {
	l.printWithContext(ctx, LevelError, err.Error(), properties)
}

// PrintFatalWithContext logs an error at FATAL level with context and terminates the application
func (l *Logger) PrintFatalWithContext(ctx context.Context, err error, properties map[string]string) {
	l.printWithContext(ctx, LevelFatal, err.Error(), properties)
	os.Exit(1)
}

// printWithContext logs a message with context information
func (l *Logger) printWithContext(ctx context.Context, level Level, message string, properties map[string]string) (int, error) {
	if level < l.minLevel {
		return 0, nil
	}

	// If properties map is nil, initialize it
	if properties == nil {
		properties = make(map[string]string)
	}

	// Extract request ID from context if available
	if requestID, ok := ctx.Value(RequestIDKey).(string); ok {
		properties["request_id"] = requestID
	}

	return l.print(level, message, properties)
}

// rotateLogFileIfNeeded checks if the log file needs rotation and rotates it if necessary
func (l *Logger) rotateLogFileIfNeeded(bytesWritten int) error {
	// If not using file logging, return immediately
	if l.config == nil || l.currentFile == nil {
		return nil
	}

	l.fileSize += int64(bytesWritten)

	// Check if we need to rotate the log file
	if l.fileSize > int64(l.config.MaxSize*1024*1024) {
		// Close current file
		if err := l.currentFile.Close(); err != nil {
			return fmt.Errorf("failed to close log file: %w", err)
		}

		// Create new log file
		logFilePath := filepath.Join(l.config.LogPath, fmt.Sprintf("app-%s.log", time.Now().Format("2006-01-02-15-04-05")))
		file, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			return fmt.Errorf("failed to open new log file: %w", err)
		}

		// Update logger state
		l.currentFile = file
		l.out = file
		l.fileSize = 0

		// Clean up old log files if needed
		if l.config.MaxBackups > 0 || l.config.MaxAge > 0 {
			go l.cleanupOldLogFiles()
		}
	}

	return nil
}

// cleanupOldLogFiles removes old log files based on MaxBackups and MaxAge settings
func (l *Logger) cleanupOldLogFiles() {
	if l.config == nil {
		return
	}

	// List all log files
	pattern := filepath.Join(l.config.LogPath, "app-*.log")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return
	}

	// Sort files by modification time (oldest first)
	type fileInfo struct {
		path    string
		modTime time.Time
	}
	files := make([]fileInfo, 0, len(matches))

	for _, match := range matches {
		info, err := os.Stat(match)
		if err != nil {
			continue
		}
		files = append(files, fileInfo{path: match, modTime: info.ModTime()})
	}

	// Sort by modification time (oldest first)
	sort.Slice(files, func(i, j int) bool {
		return files[i].modTime.Before(files[j].modTime)
	})

	// Remove files exceeding MaxBackups
	if l.config.MaxBackups > 0 && len(files) > l.config.MaxBackups {
		for i := 0; i < len(files)-l.config.MaxBackups; i++ {
			os.Remove(files[i].path)
		}
	}

	// Remove files older than MaxAge
	if l.config.MaxAge > 0 {
		cutoff := time.Now().Add(-time.Duration(l.config.MaxAge) * 24 * time.Hour)
		for _, file := range files {
			if file.modTime.Before(cutoff) {
				os.Remove(file.path)
			}
		}
	}
}

func (l *Logger) print(level Level, message string, properties map[string]string) (int, error) {
	if level < l.minLevel {
		return 0, nil
	}

	aux := struct {
		Level      string            `json:"level"`
		Time       string            `json:"time"`
		Message    string            `json:"message"`
		Properties map[string]string `json:"properties,omitempty"`
		Trace      string            `json:"trace,omitempty"`
	}{
		Level:      level.String(),
		Time:       time.Now().UTC().Format(time.RFC3339),
		Message:    message,
		Properties: properties,
	}
	// Include a stack trace for entries at the ERROR and FATAL levels.
	if level >= LevelError {
		aux.Trace = string(debug.Stack())
	}
	// Declare a line variable for holding the actual log entry text.
	var line []byte
	// Marshal the anonymous struct to JSON and store it in the line variable. If there
	// was a problem creating the JSON, set the contents of the log entry to be that
	// plain-text error message instead.
	line, err := json.Marshal(aux)
	if err != nil {
		line = []byte(LevelError.String() + ": unable to marshal log message: " + err.Error())
	}
	// Lock the mutex so that no two writes to the output destination can happen
	// concurrently. If we don't do this, it's possible that the text for two or more
	// log entries will be intermingled in the output.
	l.mu.Lock()
	defer l.mu.Unlock()

	// Write the log entry followed by a newline.
	n, err := l.out.Write(append(line, '\n'))

	// Rotate log file if needed
	if err == nil && n > 0 {
		if rotateErr := l.rotateLogFileIfNeeded(n); rotateErr != nil {
			// Just log the rotation error, don't fail the original write
			fmt.Fprintf(os.Stderr, "Log rotation error: %v\n", rotateErr)
		}
	}

	return n, err
}

// We also implement a Write() method on our Logger type so that it satisfies the
// io.Writer interface. This writes a log entry at the ERROR level with no additional
// properties.
func (l *Logger) Write(message []byte) (n int, err error) {
	return l.print(LevelError, string(message), nil)
}
