package main

import (
	"flag"
	"fmt"
	_ "github.com/lib/pq"
	"os"
	"os/signal"
	"paymentService/internal/app/grpcapp"
	"paymentService/internal/jsonlog"
	"paymentService/internal/services/payment"
	"strconv"
	"syscall"
	"time"
)

const version = "1.0.0"

type StorageDetails struct {
	DSN          string
	MaxOpenConns int
	MaxIdleConns int
	MaxIdleTime  string
}

type LogConfig struct {
	Level      string
	FilePath   string
	MaxSize    int
	MaxBackups int
	MaxAge     int
	UseJSON    bool
}

type Config struct {
	env      string
	DB       StorageDetails
	GRPC     GRPCConfig
	TokenTTL time.Duration
	Log      LogConfig
}

type GRPCConfig struct {
	Port    int
	Timeout time.Duration
}

type Application struct {
	GRPCSrv *grpcapp.App
}

func main() {
	var cfg Config

	flag.StringVar(&cfg.env, "env", "development", "Environment (development|staging|production)")

	// Database configuration
	flag.StringVar(&cfg.DB.DSN, "db-dsn", "postgres://sub:pass@localhost:5432/subscriptions?sslmode=disable&client_encoding=UTF8", "PostgresSQL DSN")
	flag.IntVar(&cfg.DB.MaxOpenConns, "db-max-open-conns", 25, "PostgresSQL max open connections")
	flag.IntVar(&cfg.DB.MaxIdleConns, "db-max-Idle-conns", 25, "PostgresSQL max Idle connections")
	flag.StringVar(&cfg.DB.MaxIdleTime, "db-max-Idle-time", "15m", "PostgresSQl max Idle time")

	// GRPC configuration
	flag.IntVar(&cfg.GRPC.Port, "grpc-port", 6000, "GRPC port")
	flag.DurationVar(&cfg.TokenTTL, "token-ttl", time.Hour, "GRPC's work duration")

	// Logging configuration
	flag.StringVar(&cfg.Log.Level, "log-level", "info", "Log level (debug|info|warn|error|fatal)")
	flag.StringVar(&cfg.Log.FilePath, "log-file-path", "./logs", "Path to log files directory")
	flag.IntVar(&cfg.Log.MaxSize, "log-max-size", 100, "Maximum size of log files in MB before rotation")
	flag.IntVar(&cfg.Log.MaxBackups, "log-max-backups", 5, "Maximum number of old log files to retain")
	flag.IntVar(&cfg.Log.MaxAge, "log-max-age", 30, "Maximum number of days to retain old log files")
	flag.BoolVar(&cfg.Log.UseJSON, "log-use-json", true, "Use JSON format for logs")

	flag.Parse()

	// Initialize logger based on configuration
	var logger *jsonlog.Logger
	var err error

	// Parse log level
	var level jsonlog.Level
	switch cfg.Log.Level {
	case "debug":
		level = jsonlog.LevelDebug
	case "info":
		level = jsonlog.LevelInfo
	case "warn":
		level = jsonlog.LevelWarn
	case "error":
		level = jsonlog.LevelError
	case "fatal":
		level = jsonlog.LevelFatal
	default:
		level = jsonlog.LevelInfo
	}

	// Use file logger in production and staging environments
	if cfg.env == "production" || cfg.env == "staging" {
		// Configure log rotation
		logConfig := jsonlog.LogConfig{
			LogPath:    cfg.Log.FilePath,
			MaxSize:    cfg.Log.MaxSize,
			MaxBackups: cfg.Log.MaxBackups,
			MaxAge:     cfg.Log.MaxAge,
			Compress:   true,
		}

		logger, err = jsonlog.NewFileLogger(logConfig, level)
		if err != nil {
			// Fall back to stdout logging if file logging fails
			fmt.Fprintf(os.Stderr, "Failed to initialize file logger: %v\n", err)
			logger = jsonlog.New(os.Stdout, level)
		}
	} else {
		// Use stdout logger for development
		logger = jsonlog.New(os.Stdout, level)
	}

	logger.PrintInfo("Starting payment service", map[string]string{
		"version":     version,
		"environment": cfg.env,
	})

	app := New(logger, cfg.GRPC.Port, cfg.TokenTTL)

	logger.PrintInfo("connection pool established", map[string]string{
		"port": strconv.Itoa(cfg.GRPC.Port),
	})
	go app.GRPCSrv.MustRun()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)

	sign := <-stop
	logger.PrintInfo("stopping application", map[string]string{
		"signal": sign.String(),
	})

	app.GRPCSrv.Stop()
}

func New(log *jsonlog.Logger, grpcPort int, tokenTTL time.Duration) *Application {
	stripeKey := "key"
	subscriptionService := payment.New(log, tokenTTL, stripeKey)
	grpcApp := grpcapp.New(log, grpcPort, subscriptionService) // добавить сервис

	return &Application{GRPCSrv: grpcApp}
}
