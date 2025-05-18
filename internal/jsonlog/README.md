# Enhanced JSON Logger

This package provides a structured JSON logging system with support for different log levels, context-aware logging, and log file rotation.

## Features

- Multiple log levels (DEBUG, INFO, WARN, ERROR, FATAL)
- Structured JSON output for easy parsing
- Context-aware logging with request ID tracking
- Log file rotation based on file size
- Automatic cleanup of old log files
- Stack traces for ERROR and FATAL levels

## Usage

### Basic Usage

```go
// Create a logger that writes to stdout with minimum level INFO
logger := jsonlog.New(os.Stdout, jsonlog.LevelInfo)

// Log messages at different levels
logger.PrintDebug("Debug message", nil)  // Won't be printed if minLevel is INFO
logger.PrintInfo("Info message", nil)
logger.PrintWarn("Warning message", nil)
logger.PrintError(errors.New("error occurred"), nil)
```

### With Properties

```go
// Add structured properties to your logs
logger.PrintInfo("User logged in", map[string]string{
    "user_id": "12345",
    "ip_address": "192.168.1.1",
})
```

### Context-Aware Logging

```go
// Create a context with request ID
ctx := context.WithValue(context.Background(), jsonlog.RequestIDKey, "req-123")

// Log with context
logger.PrintInfoWithContext(ctx, "Processing request", map[string]string{
    "endpoint": "/api/users",
})
```

### File Logging with Rotation

```go
// Configure log rotation
config := jsonlog.LogConfig{
    LogPath:    "./logs",
    MaxSize:    10,       // 10 MB
    MaxBackups: 5,        // Keep 5 old files
    MaxAge:     7,        // 7 days
    Compress:   true,
}

// Create a file logger
logger, err := jsonlog.NewFileLogger(config, jsonlog.LevelInfo)
if err != nil {
    panic(err)
}

// Use the logger as normal
logger.PrintInfo("Application started", nil)
```

## Log Levels

- **DEBUG**: Detailed information, typically useful only when diagnosing problems
- **INFO**: Confirmation that things are working as expected
- **WARN**: Indication that something unexpected happened, but the application can continue
- **ERROR**: Due to a more serious problem, the application has not been able to perform a function
- **FATAL**: A severe error that causes the application to terminate

## Best Practices

1. **Use structured logging**: Always include relevant properties in your log messages
2. **Include request IDs**: Use context-aware logging to track requests through your system
3. **Be consistent with log levels**: Use appropriate log levels for different types of messages
4. **Log important operations**: Log the start and end of important operations
5. **Include error details**: When logging errors, include all relevant details