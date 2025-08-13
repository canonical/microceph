package logger

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	lxdlogger "github.com/canonical/lxd/shared/logger"
)

var (
	// Global DaemonLogger singleton instance
	DaemonLogger *MicroCephDaemonLogger
)

// MicroCephDaemonLogger wraps slog.Logger with additional functionality
type MicroCephDaemonLogger struct {
	logger     *slog.Logger
	levelVar   *slog.LevelVar
	configPath string
	mu         sync.RWMutex
}

// NewLogger creates a new MicroCephDaemonLogger instance
func NewLogger(configFilePath string) (*MicroCephDaemonLogger, error) {
	levelVar := new(slog.LevelVar)
	levelVar.Set(slog.LevelInfo) // Default to info level

	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: levelVar,
	})

	logger := &MicroCephDaemonLogger{
		logger:     slog.New(handler),
		levelVar:   levelVar,
		configPath: configFilePath,
	}

	// Load config if path is provided
	if configFilePath != "" {
		logger.mu.Lock()
		// Check if config file exists, if not create default
		_, statErr := os.Stat(configFilePath)
		if os.IsNotExist(statErr) {
			if createErr := logger.createDefaultConfig(); createErr != nil {
				logger.mu.Unlock()
				return nil, fmt.Errorf("failed to create default config: %w", createErr)
			}
		}
		err := logger.loadConfigLocked()
		if err != nil {
			logger.mu.Unlock()
			return nil, fmt.Errorf("failed to load config: %w", err)
		}
		logger.mu.Unlock()
	}

	return logger, nil
}

type LogConfig struct {
	Level string `json:"level"`
}

// createDefaultConfig creates a default config file with info level
func (l *MicroCephDaemonLogger) createDefaultConfig() error {
	config := LogConfig{
		Level: "info",
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal default config: %w", err)
	}

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(l.configPath), 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	if err := os.WriteFile(l.configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write default config: %w", err)
	}

	return nil
}

// loadConfigLocked reads log level from config file (must be called with mutex held)
func (l *MicroCephDaemonLogger) loadConfigLocked() error {
	if l.configPath == "" {
		// Not initialized with a config path, skip loading
		return nil
	}

	data, err := os.ReadFile(l.configPath)
	if os.IsNotExist(err) {
		// Config file doesn't exist, use default
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to read log config: %w", err)
	}

	var config LogConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("failed to parse log config: %w", err)
	}

	level, err := StringToLevel(config.Level)
	if err != nil {
		return fmt.Errorf("invalid log level in config: %w", err)
	}

	l.levelVar.Set(level)
	return nil
}

// saveConfig writes current log level to config file
func (l *MicroCephDaemonLogger) saveConfig() error {
	if l.configPath == "" {
		// No config path provided, skip saving
		return nil
	}

	lvl, err := LevelToString(l.levelVar.Level())
	if err != nil {
		return fmt.Errorf("failed to convert log level to string: %w", err)
	}
	config := LogConfig{
		Level: lvl,
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal log config: %w", err)
	}

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(l.configPath), 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	if err := os.WriteFile(l.configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write log config: %w", err)
	}

	return nil
}

// SetLevel updates the log level and persists it
func (l *MicroCephDaemonLogger) SetLevel(level string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Parse the new level
	parsedLevel, err := StringToLevel(level)
	if err != nil {
		return fmt.Errorf("invalid log level: %w", err)
	}
	l.levelVar.Set(slog.Level(parsedLevel))

	// Recreate the logger with the new level
	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: l.levelVar,
	})
	l.logger = slog.New(handler)

	return l.saveConfig()
}

// GetLevel returns current log level
func (l *MicroCephDaemonLogger) GetLevel() string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	s, err := LevelToString(l.levelVar.Level())
	if err != nil {
		return "info" // fallback to default level if conversion fails
	}
	return s
}

func SetLevel(level string) error {
	if DaemonLogger == nil {
		return fmt.Errorf("logger not initialized - call NewLogger() first and assign to DaemonLogger")
	}
	err := DaemonLogger.SetLevel(level)
	return err
}

func GetLevel() string {
	if DaemonLogger == nil {
		return "info" // default level if logger not initialized
	}
	return DaemonLogger.GetLevel()
}

// legacyLvlS2I maps string levels to legacy int levels
var legacyLvlS2I = map[string]int{
	"trace":   6,
	"debug":   5,
	"info":    4,
	"warning": 3,
	"warn":    3, // "warning" is an alias for "warn"
	"error":   2,
	"fatal":   1,
	"panic":   0,
	"6":       6,
	"5":       5,
	"4":       4,
	"3":       3,
	"2":       2,
	"1":       1,
	"0":       0,
}

// legacyLvlI2S maps legacy int levels to string
var legacyLvlI2S = map[int]string{
	6: "trace",
	5: "debug",
	4: "info",
	3: "warn",
	2: "error",
	1: "fatal",
	0: "panic",
}

// lvli2s maps int levels to string
var lvli2s = map[slog.Level]string{
	slog.Level(-8):  "trace",
	slog.LevelDebug: "debug",
	slog.LevelInfo:  "info",
	slog.LevelWarn:  "warn",
	slog.LevelError: "error",
	slog.Level(16):  "fatal",
	slog.Level(32):  "panic",
}

// lvls2i maps string levels to slog.Level
var lvls2i = map[string]slog.Level{
	"trace": slog.Level(-8),
	"debug": slog.LevelDebug,
	"info":  slog.LevelInfo,
	"warn":  slog.LevelWarn,
	"error": slog.LevelError,
	"fatal": slog.Level(16),
	"panic": slog.Level(32),
}

// ParseLegacyLevels converts string to int log level
func ParseLegacyLevels(s string) (int, error) {
	if level, ok := legacyLvlS2I[s]; ok {
		return level, nil
	}
	return 4, fmt.Errorf("unknown level: %s", s)
}

// ParseLegacyLevelsInt converts int to string log level
func ParseLegacyLevelsInt(i int) string {
	if level, ok := legacyLvlI2S[i]; ok {
		return level
	}
	return "info" // Fallback to "info" for unknown levels
}

// LevelToString converts level to string
func LevelToString(l slog.Level) (string, error) {
	if level, ok := lvli2s[l]; ok {
		return level, nil
	}
	return "info", fmt.Errorf("unknown level: %d", l)
}

// StringToLevel converts string to slog.Level
func StringToLevel(s string) (slog.Level, error) {
	if level, ok := lvls2i[s]; ok {
		return level, nil
	}
	return slog.LevelInfo, fmt.Errorf("unknown level: %s", s)
}

// MicroCephDaemonLogger methods
func (l *MicroCephDaemonLogger) Debugf(format string, args ...interface{}) {
	if l.logger.Enabled(nil, slog.LevelDebug) {
		l.logger.Debug(fmt.Sprintf(format, args...))
	}
}

func (l *MicroCephDaemonLogger) Infof(format string, args ...interface{}) {
	if l.logger.Enabled(nil, slog.LevelInfo) {
		l.logger.Info(fmt.Sprintf(format, args...))
	}
}

func (l *MicroCephDaemonLogger) Warnf(format string, args ...interface{}) {
	if l.logger.Enabled(nil, slog.LevelWarn) {
		l.logger.Warn(fmt.Sprintf(format, args...))
	}
}

func (l *MicroCephDaemonLogger) Errorf(format string, args ...interface{}) {
	if l.logger.Enabled(nil, slog.LevelError) {
		l.logger.Error(fmt.Sprintf(format, args...))
	}
}

func (l *MicroCephDaemonLogger) Debug(msg string) {
	l.logger.Debug(msg)
}

func (l *MicroCephDaemonLogger) Info(msg string) {
	l.logger.Info(msg)
}

func (l *MicroCephDaemonLogger) Warn(msg string) {
	l.logger.Warn(msg)
}

func (l *MicroCephDaemonLogger) Error(msg string) {
	l.logger.Error(msg)
}

func (l *MicroCephDaemonLogger) DebugKV(msg string, keysAndValues ...interface{}) {
	l.logger.Debug(msg, keysAndValues...)
}

func (l *MicroCephDaemonLogger) InfoKV(msg string, keysAndValues ...interface{}) {
	l.logger.Info(msg, keysAndValues...)
}

func (l *MicroCephDaemonLogger) WarnKV(msg string, keysAndValues ...interface{}) {
	l.logger.Warn(msg, keysAndValues...)
}

func (l *MicroCephDaemonLogger) ErrorKV(msg string, keysAndValues ...interface{}) {
	l.logger.Error(msg, keysAndValues...)
}

// Global wrapper functions for backward compatibility
func Debugf(format string, args ...interface{}) {
	if DaemonLogger != nil {
		DaemonLogger.Debugf(format, args...)
	}
}

func Infof(format string, args ...interface{}) {
	if DaemonLogger != nil {
		DaemonLogger.Infof(format, args...)
	}
}

func Warnf(format string, args ...interface{}) {
	if DaemonLogger != nil {
		DaemonLogger.Warnf(format, args...)
	}
}

func Errorf(format string, args ...interface{}) {
	if DaemonLogger != nil {
		DaemonLogger.Errorf(format, args...)
	}
}

func Debug(msg string) {
	if DaemonLogger != nil {
		DaemonLogger.Debug(msg)
	}
}

func Info(msg string) {
	if DaemonLogger != nil {
		DaemonLogger.Info(msg)
	}
}

func Warn(msg string) {
	if DaemonLogger != nil {
		DaemonLogger.Warn(msg)
	}
}

func Error(msg string) {
	if DaemonLogger != nil {
		DaemonLogger.Error(msg)
	}
}

func DebugKV(msg string, keysAndValues ...interface{}) {
	if DaemonLogger != nil {
		DaemonLogger.DebugKV(msg, keysAndValues...)
	}
}

func InfoKV(msg string, keysAndValues ...interface{}) {
	if DaemonLogger != nil {
		DaemonLogger.InfoKV(msg, keysAndValues...)
	}
}

func WarnKV(msg string, keysAndValues ...interface{}) {
	if DaemonLogger != nil {
		DaemonLogger.WarnKV(msg, keysAndValues...)
	}
}

func ErrorKV(msg string, keysAndValues ...interface{}) {
	if DaemonLogger != nil {
		DaemonLogger.ErrorKV(msg, keysAndValues...)
	}
}

// LXDLoggerAdapter wraps MicroCephDaemonLogger to implement the LXD Logger interface
type LXDLoggerAdapter struct {
	logger *MicroCephDaemonLogger
}

// NewLXDLoggerAdapter creates a new adapter that implements the LXD Logger interface
func NewLXDLoggerAdapter(logger *MicroCephDaemonLogger) *LXDLoggerAdapter {
	return &LXDLoggerAdapter{
		logger: logger,
	}
}

// convertCtxToKV converts LXD context arguments to key-value pairs for slog
func convertCtxToKV(args ...lxdlogger.Ctx) []interface{} {
	if len(args) == 0 {
		return nil
	}
	
	kvPairs := make([]interface{}, 0, len(args)*2)
	for _, ctx := range args {
		for k, v := range ctx {
			kvPairs = append(kvPairs, k, v)
		}
	}
	return kvPairs
}

// logWithCtx is a helper that logs a message with optional context using the appropriate method
func (a *LXDLoggerAdapter) logWithCtx(msg string, logFunc func(string), logKVFunc func(string, ...interface{}), args ...lxdlogger.Ctx) {
	if kvPairs := convertCtxToKV(args...); kvPairs != nil {
		logKVFunc(msg, kvPairs...)
	} else {
		logFunc(msg)
	}
}

// Panic implements the LXD Logger interface
func (a *LXDLoggerAdapter) Panic(msg string, args ...lxdlogger.Ctx) {
	// Use Error level since we don't have Panic
	a.logWithCtx(msg, a.logger.Error, a.logger.ErrorKV, args...)
}

// Fatal implements the LXD Logger interface
func (a *LXDLoggerAdapter) Fatal(msg string, args ...lxdlogger.Ctx) {
	// Use Error level since we don't have Fatal
	a.logWithCtx(msg, a.logger.Error, a.logger.ErrorKV, args...)
}

// Error implements the LXD Logger interface
func (a *LXDLoggerAdapter) Error(msg string, args ...lxdlogger.Ctx) {
	a.logWithCtx(msg, a.logger.Error, a.logger.ErrorKV, args...)
}

// Warn implements the LXD Logger interface
func (a *LXDLoggerAdapter) Warn(msg string, args ...lxdlogger.Ctx) {
	a.logWithCtx(msg, a.logger.Warn, a.logger.WarnKV, args...)
}

// Info implements the LXD Logger interface
func (a *LXDLoggerAdapter) Info(msg string, args ...lxdlogger.Ctx) {
	a.logWithCtx(msg, a.logger.Info, a.logger.InfoKV, args...)
}

// Debug implements the LXD Logger interface
func (a *LXDLoggerAdapter) Debug(msg string, args ...lxdlogger.Ctx) {
	a.logWithCtx(msg, a.logger.Debug, a.logger.DebugKV, args...)
}

// Trace implements the LXD Logger interface
func (a *LXDLoggerAdapter) Trace(msg string, args ...lxdlogger.Ctx) {
	// Since MicroCephDaemonLogger doesn't have Trace, use Debug
	a.logWithCtx(msg, a.logger.Debug, a.logger.DebugKV, args...)
}

// AddContext implements the LXD Logger interface
func (a *LXDLoggerAdapter) AddContext(ctx lxdlogger.Ctx) lxdlogger.Logger {
	// For simplicity, return a new adapter that prefixes messages with context
	return &contextualLXDLoggerAdapter{
		adapter: a,
		context: ctx,
	}
}

// contextualLXDLoggerAdapter handles context by prefixing it to log messages
type contextualLXDLoggerAdapter struct {
	adapter *LXDLoggerAdapter
	context lxdlogger.Ctx
}

// mergeContexts combines the stored context with additional arguments
func (c *contextualLXDLoggerAdapter) mergeContexts(args ...lxdlogger.Ctx) []lxdlogger.Ctx {
	return append([]lxdlogger.Ctx{c.context}, args...)
}

func (c *contextualLXDLoggerAdapter) Panic(msg string, args ...lxdlogger.Ctx) {
	c.adapter.Panic(msg, c.mergeContexts(args...)...)
}

func (c *contextualLXDLoggerAdapter) Fatal(msg string, args ...lxdlogger.Ctx) {
	c.adapter.Fatal(msg, c.mergeContexts(args...)...)
}

func (c *contextualLXDLoggerAdapter) Error(msg string, args ...lxdlogger.Ctx) {
	c.adapter.Error(msg, c.mergeContexts(args...)...)
}

func (c *contextualLXDLoggerAdapter) Warn(msg string, args ...lxdlogger.Ctx) {
	c.adapter.Warn(msg, c.mergeContexts(args...)...)
}

func (c *contextualLXDLoggerAdapter) Info(msg string, args ...lxdlogger.Ctx) {
	c.adapter.Info(msg, c.mergeContexts(args...)...)
}

func (c *contextualLXDLoggerAdapter) Debug(msg string, args ...lxdlogger.Ctx) {
	c.adapter.Debug(msg, c.mergeContexts(args...)...)
}

func (c *contextualLXDLoggerAdapter) Trace(msg string, args ...lxdlogger.Ctx) {
	c.adapter.Trace(msg, c.mergeContexts(args...)...)
}

func (c *contextualLXDLoggerAdapter) AddContext(ctx lxdlogger.Ctx) lxdlogger.Logger {
	// Merge contexts
	mergedCtx := make(lxdlogger.Ctx)
	for k, v := range c.context {
		mergedCtx[k] = v
	}
	for k, v := range ctx {
		mergedCtx[k] = v
	}
	return &contextualLXDLoggerAdapter{
		adapter: c.adapter,
		context: mergedCtx,
	}
}
