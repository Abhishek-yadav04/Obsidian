// Package logging provides structured logging for Obsidian WAF.
// This implementation addresses the critical audit finding of unstructured printf logging
// and provides proper observability with request tracing.
package logging

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// contextKey type for context values
type contextKey string

const (
	// RequestIDKey is the context key for request ID
	RequestIDKey contextKey = "request_id"

	// UserIDKey is the context key for user ID
	UserIDKey contextKey = "user_id"

	// ClientIPKey is the context key for client IP
	ClientIPKey contextKey = "client_ip"
)

var (
	// Global logger instance
	globalLogger *Logger
	initOnce     sync.Once
)

// Config holds logging configuration
type Config struct {
	// Level is the minimum log level (debug, info, warn, error)
	Level string

	// Format is the output format (json, console)
	Format string

	// Output is where logs are written (stdout, stderr, or file path)
	Output string

	// EnableCaller adds caller information to logs
	EnableCaller bool

	// EnableStacktrace adds stacktrace for error level
	EnableStacktrace bool

	// SensitiveFields are fields to redact from logs
	SensitiveFields []string

	// SamplingEnabled enables log sampling for high-volume scenarios
	SamplingEnabled bool

	// SamplingInitial is the number of entries before sampling kicks in
	SamplingInitial int

	// SamplingThereafter is the sampling rate after initial entries
	SamplingThereafter int
}

// DefaultConfig returns production-ready defaults
func DefaultConfig() Config {
	return Config{
		Level:              "info",
		Format:             "json",
		Output:             "stdout",
		EnableCaller:       true,
		EnableStacktrace:   true,
		SensitiveFields:    []string{"password", "token", "secret", "key", "authorization", "cookie", "session"},
		SamplingEnabled:    false,
		SamplingInitial:    100,
		SamplingThereafter: 100,
	}
}

// Logger wraps zap.Logger with additional functionality
type Logger struct {
	*zap.Logger
	config Config
}

// Init initializes the global logger
func Init(cfg Config) error {
	var err error
	initOnce.Do(func() {
		globalLogger, err = NewLogger(cfg)
	})
	return err
}

// NewLogger creates a new structured logger
func NewLogger(cfg Config) (*Logger, error) {
	// Parse log level
	var level zapcore.Level
	if err := level.UnmarshalText([]byte(cfg.Level)); err != nil {
		level = zapcore.InfoLevel
	}

	// Configure encoder
	var encoder zapcore.Encoder
	encoderConfig := zapcore.EncoderConfig{
		TimeKey:        "timestamp",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		FunctionKey:    zapcore.OmitKey,
		MessageKey:     "message",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.LowercaseLevelEncoder,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.MillisDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}

	switch cfg.Format {
	case "console":
		encoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
		encoder = zapcore.NewConsoleEncoder(encoderConfig)
	default:
		encoder = zapcore.NewJSONEncoder(encoderConfig)
	}

	// Configure output
	var output zapcore.WriteSyncer
	switch cfg.Output {
	case "stdout", "":
		output = zapcore.AddSync(os.Stdout)
	case "stderr":
		output = zapcore.AddSync(os.Stderr)
	default:
		file, err := os.OpenFile(cfg.Output, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return nil, fmt.Errorf("failed to open log file: %w", err)
		}
		output = zapcore.AddSync(file)
	}

	// Build core
	core := zapcore.NewCore(encoder, output, level)

	// Add sampling if enabled
	if cfg.SamplingEnabled {
		core = zapcore.NewSamplerWithOptions(core, time.Second, cfg.SamplingInitial, cfg.SamplingThereafter)
	}

	// Build options
	opts := []zap.Option{}
	if cfg.EnableCaller {
		opts = append(opts, zap.AddCaller())
	}
	if cfg.EnableStacktrace {
		opts = append(opts, zap.AddStacktrace(zapcore.ErrorLevel))
	}

	logger := zap.New(core, opts...)

	return &Logger{
		Logger: logger,
		config: cfg,
	}, nil
}

// Get returns the global logger
func Get() *Logger {
	if globalLogger == nil {
		// Initialize with defaults if not already initialized
		_ = Init(DefaultConfig())
	}
	return globalLogger
}

// WithContext returns a logger with context values
func (l *Logger) WithContext(ctx context.Context) *Logger {
	fields := []zap.Field{}

	if requestID, ok := ctx.Value(RequestIDKey).(string); ok {
		fields = append(fields, zap.String("request_id", requestID))
	}
	if userID, ok := ctx.Value(UserIDKey).(int); ok {
		fields = append(fields, zap.Int("user_id", userID))
	}
	if clientIP, ok := ctx.Value(ClientIPKey).(string); ok {
		fields = append(fields, zap.String("client_ip", clientIP))
	}

	return &Logger{
		Logger: l.Logger.With(fields...),
		config: l.config,
	}
}

// WithRequestID returns a logger with request ID
func (l *Logger) WithRequestID(requestID string) *Logger {
	return &Logger{
		Logger: l.Logger.With(zap.String("request_id", requestID)),
		config: l.config,
	}
}

// WithFields returns a logger with additional fields
func (l *Logger) WithFields(fields map[string]interface{}) *Logger {
	zapFields := make([]zap.Field, 0, len(fields))
	for k, v := range fields {
		// Redact sensitive fields
		if l.isSensitive(k) {
			zapFields = append(zapFields, zap.String(k, "[REDACTED]"))
		} else {
			zapFields = append(zapFields, zap.Any(k, v))
		}
	}

	return &Logger{
		Logger: l.Logger.With(zapFields...),
		config: l.config,
	}
}

// isSensitive checks if a field name should be redacted
func (l *Logger) isSensitive(field string) bool {
	for _, sensitive := range l.config.SensitiveFields {
		if field == sensitive {
			return true
		}
	}
	return false
}

// Sync flushes any buffered log entries
func (l *Logger) Sync() error {
	return l.Logger.Sync()
}

// HTTP-specific logging methods

// LogRequest logs an incoming HTTP request
func (l *Logger) LogRequest(requestID, method, path, clientIP, userAgent string, statusCode int, duration time.Duration) {
	l.Info("http_request",
		zap.String("request_id", requestID),
		zap.String("method", method),
		zap.String("path", path),
		zap.String("client_ip", clientIP),
		zap.String("user_agent", userAgent),
		zap.Int("status_code", statusCode),
		zap.Duration("duration", duration),
	)
}

// LogWAFBlock logs a WAF block event
func (l *Logger) LogWAFBlock(requestID string, ruleID int, action, clientIP, method, uri, details string) {
	l.Warn("waf_block",
		zap.String("request_id", requestID),
		zap.Int("rule_id", ruleID),
		zap.String("action", action),
		zap.String("client_ip", clientIP),
		zap.String("method", method),
		zap.String("uri", uri),
		zap.String("details", details),
	)
}

// LogSecurityEvent logs a security-related event
func (l *Logger) LogSecurityEvent(eventType, clientIP, details string, severity int) {
	l.Warn("security_event",
		zap.String("event_type", eventType),
		zap.String("client_ip", clientIP),
		zap.String("details", details),
		zap.Int("severity", severity),
	)
}

// LogAuth logs an authentication event
func (l *Logger) LogAuth(username, clientIP, result string, success bool) {
	if success {
		l.Info("auth_success",
			zap.String("username", username),
			zap.String("client_ip", clientIP),
		)
	} else {
		l.Warn("auth_failure",
			zap.String("username", username),
			zap.String("client_ip", clientIP),
			zap.String("reason", result),
		)
	}
}

// LogRateLimit logs a rate limiting event
func (l *Logger) LogRateLimit(clientIP, endpoint string, blocked bool) {
	l.Warn("rate_limit",
		zap.String("client_ip", clientIP),
		zap.String("endpoint", endpoint),
		zap.Bool("blocked", blocked),
	)
}

// Writer returns an io.Writer for integration with http.Server error logging
func (l *Logger) Writer() io.Writer {
	return &logWriter{logger: l}
}

// logWriter adapts Logger to io.Writer
type logWriter struct {
	logger *Logger
}

func (w *logWriter) Write(p []byte) (int, error) {
	w.logger.Error(string(p))
	return len(p), nil
}

// RequestLogger provides per-request logging
type RequestLogger struct {
	logger    *Logger
	requestID string
	startTime time.Time
	method    string
	path      string
	clientIP  string
	userAgent string
}

// NewRequestLogger creates a logger for a specific request
func NewRequestLogger(logger *Logger, requestID, method, path, clientIP, userAgent string) *RequestLogger {
	return &RequestLogger{
		logger:    logger.WithRequestID(requestID),
		requestID: requestID,
		startTime: time.Now(),
		method:    method,
		path:      path,
		clientIP:  clientIP,
		userAgent: userAgent,
	}
}

// Debug logs a debug message
func (rl *RequestLogger) Debug(msg string, fields ...zap.Field) {
	rl.logger.Debug(msg, fields...)
}

// Info logs an info message
func (rl *RequestLogger) Info(msg string, fields ...zap.Field) {
	rl.logger.Info(msg, fields...)
}

// Warn logs a warning message
func (rl *RequestLogger) Warn(msg string, fields ...zap.Field) {
	rl.logger.Warn(msg, fields...)
}

// Error logs an error message
func (rl *RequestLogger) Error(msg string, fields ...zap.Field) {
	rl.logger.Error(msg, fields...)
}

// Complete logs the completed request with duration
func (rl *RequestLogger) Complete(statusCode int) {
	rl.logger.LogRequest(
		rl.requestID,
		rl.method,
		rl.path,
		rl.clientIP,
		rl.userAgent,
		statusCode,
		time.Since(rl.startTime),
	)
}

// Metrics helper methods

// IncrementCounter logs a metric increment (for external metric systems)
func (l *Logger) IncrementCounter(name string, value int64, labels map[string]string) {
	fields := []zap.Field{
		zap.String("metric_name", name),
		zap.Int64("value", value),
	}
	for k, v := range labels {
		fields = append(fields, zap.String("label_"+k, v))
	}
	l.Debug("metric_counter", fields...)
}

// RecordHistogram logs a histogram metric
func (l *Logger) RecordHistogram(name string, value float64, labels map[string]string) {
	fields := []zap.Field{
		zap.String("metric_name", name),
		zap.Float64("value", value),
	}
	for k, v := range labels {
		fields = append(fields, zap.String("label_"+k, v))
	}
	l.Debug("metric_histogram", fields...)
}
