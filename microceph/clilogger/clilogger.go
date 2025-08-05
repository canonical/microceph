package clilogger

import (
	"fmt"
	"log/slog"
	"os"
)

var (
	// Global CLI logger instance
	Logger *slog.Logger
)

// InitLogger initializes the CLI logger based on flags
// Must be called before using the logger
func InitLogger(debug, verbose bool) {
	var level slog.Level

	if debug {
		level = slog.LevelDebug
	} else if verbose {
		level = slog.LevelInfo
	} else {
		level = slog.LevelWarn // Only warnings and errors by default
	}

	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	})

	Logger = slog.New(handler)
}

// Convenience functions
func Debug(msg string, args ...any) {
	if Logger != nil {
		Logger.Debug(msg, args...)
	}
}

func Info(msg string, args ...any) {
	if Logger != nil {
		Logger.Info(msg, args...)
	}
}

func Warn(msg string, args ...any) {
	if Logger != nil {
		Logger.Warn(msg, args...)
	}
}

func Error(msg string, args ...any) {
	if Logger != nil {
		Logger.Error(msg, args...)
	}
}

func Debugf(format string, args ...any) {
	if Logger != nil && Logger.Enabled(nil, slog.LevelDebug) {
		Logger.Debug(fmt.Sprintf(format, args...))
	}
}

func Infof(format string, args ...any) {
	if Logger != nil && Logger.Enabled(nil, slog.LevelInfo) {
		Logger.Info(fmt.Sprintf(format, args...))
	}
}

func Warnf(format string, args ...any) {
	if Logger != nil && Logger.Enabled(nil, slog.LevelWarn) {
		Logger.Warn(fmt.Sprintf(format, args...))
	}
}

func Errorf(format string, args ...any) {
	if Logger != nil && Logger.Enabled(nil, slog.LevelError) {
		Logger.Error(fmt.Sprintf(format, args...))
	}
}
