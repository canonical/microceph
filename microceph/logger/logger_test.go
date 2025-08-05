package logger

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewLogger(t *testing.T) {
	// Test creating logger without config file
	logger, err := NewLogger("")
	if err != nil {
		t.Fatalf("Failed to create logger without config: %v", err)
	}
	if logger == nil {
		t.Fatal("Logger should not be nil")
	}
	lvl := logger.GetLevel()
	if lvl != "info" {
		t.Errorf("Expected default level to be info, got %v", lvl)
	}

	// Test: create logger with config file
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "test-config.json")

	// Create a config file
	config := LogConfig{Level: "debug"}
	data, _ := json.Marshal(config)
	err = os.WriteFile(configPath, data, 0644)
	if err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	logger, err = NewLogger(configPath)
	if err != nil {
		t.Fatalf("Failed to create logger with config: %v", err)
	}
	// We configured debug in config, expect to show up here
	if logger.GetLevel() != "debug" {
		t.Errorf("Expected level to be Debug, got %v", logger.GetLevel())
	}
}

func TestNewLoggerCreatesDefaultConfig(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "new-config.json")

	// Config file doesn't exist, should create default
	logger, err := NewLogger(configPath)
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}

	// Check that config file was created
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Error("Default config file was not created")
	}

	// Check config content
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("Failed to read created config: %v", err)
	}

	var config LogConfig
	err = json.Unmarshal(data, &config)
	if err != nil {
		t.Fatalf("Failed to parse created config: %v", err)
	}

	if config.Level != "info" {
		t.Errorf("Expected default level 'info', got '%s'", config.Level)
	}

	if logger.GetLevel() != "info" {
		t.Errorf("Expected logger level to be Info, got %v", logger.GetLevel())
	}
}

func TestSetLevel(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "level-test.json")

	logger, err := NewLogger(configPath)
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}

	// Test setting different levels
	levels := []string{"debug", "info", "warn", "error"}

	for _, level := range levels {
		err = logger.SetLevel(level)
		if err != nil {
			t.Errorf("Failed to set level %v: %v", level, err)
		}

		if logger.GetLevel() != level {
			t.Errorf("Expected level %v, got %v", level, logger.GetLevel())
		}

		// Check that config was saved
		data, err := os.ReadFile(configPath)
		if err != nil {
			t.Errorf("Failed to read config after setting level: %v", err)
			continue
		}

		var config LogConfig
		err = json.Unmarshal(data, &config)
		if err != nil {
			t.Errorf("Failed to parse config: %v", err)
			continue
		}
	}
}

func TestLoggerMethods(t *testing.T) {
	// Capture output
	var buf bytes.Buffer

	// Create logger with custom handler that writes to buffer
	levelVar := new(slog.LevelVar)
	levelVar.Set(slog.LevelDebug) // Set to debug to capture all messages

	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: levelVar,
	})

	logger := &MicroCephDaemonLogger{
		logger:   slog.New(handler),
		levelVar: levelVar,
	}

	// Test formatted methods
	logger.Debugf("debug message %s", "test")
	logger.Infof("info message %d", 42)
	logger.Warnf("warn message %t", true)
	logger.Errorf("error message %s %d", "test", 123)

	// Test simple methods
	logger.Debug("debug message")
	logger.Info("info message")
	logger.Warn("warn message")
	logger.Error("error message")

	// Test key-value methods
	logger.DebugKV("debug with kv", "key1", "value1")
	logger.InfoKV("info with kv", "key2", "value2")
	logger.WarnKV("warn with kv", "key3", "value3")
	logger.ErrorKV("error with kv", "key4", "value4")

	output := buf.String()

	// Check that all messages were logged
	expectedMessages := []string{
		"debug message test",
		"info message 42",
		"warn message true",
		"error message test 123",
		"debug message",
		"info message",
		"warn message",
		"error message",
		"debug with kv",
		"info with kv",
		"warn with kv",
		"error with kv",
	}

	for _, msg := range expectedMessages {
		if !strings.Contains(output, msg) {
			t.Errorf("Expected output to contain '%s', but it didn't. Output: %s", msg, output)
		}
	}

	// Check key-value pairs
	kvPairs := []string{"key1=value1", "key2=value2", "key3=value3", "key4=value4"}
	for _, kv := range kvPairs {
		if !strings.Contains(output, kv) {
			t.Errorf("Expected output to contain '%s', but it didn't", kv)
		}
	}
}

func TestLoggerLevelFiltering(t *testing.T) {
	var buf bytes.Buffer

	// Create logger with INFO level
	levelVar := new(slog.LevelVar)
	levelVar.Set(slog.LevelInfo)

	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: levelVar,
	})

	logger := &MicroCephDaemonLogger{
		logger:   slog.New(handler),
		levelVar: levelVar,
	}

	// Log messages at different levels
	logger.Debug("debug message") // Should be filtered out
	logger.Info("info message")   // Should appear
	logger.Warn("warn message")   // Should appear
	logger.Error("error message") // Should appear

	output := buf.String()

	// Debug should be filtered out
	if strings.Contains(output, "debug message") {
		t.Error("Debug message should have been filtered out")
	}

	// Others should appear
	expectedMessages := []string{"info message", "warn message", "error message"}
	for _, msg := range expectedMessages {
		if !strings.Contains(output, msg) {
			t.Errorf("Expected output to contain '%s'", msg)
		}
	}
}

func TestGlobalFunctions(t *testing.T) {
	// Save original state
	originalDefaultLogger := DaemonLogger
	defer func() {
		DaemonLogger = originalDefaultLogger
	}()

	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "global-test.json")

	// Test setting DaemonLogger directly
	var err error
	DaemonLogger, err = NewLogger(configPath)
	if err != nil {
		t.Fatalf("Failed to initialize global logger: %v", err)
	}

	if DaemonLogger == nil {
		t.Error("DaemonLogger should not be nil after assignment")
	}

	// Test global SetLevel
	err = SetLevel("debug")
	if err != nil {
		t.Errorf("Failed to set global level: %v", err)
	}

	if GetLevel() != "debug" {
		t.Errorf("Expected global level to be Debug, got %v", GetLevel())
	}

	// Test global logging functions with captured output
	var buf bytes.Buffer

	// Replace the logger's output temporarily
	levelVar := new(slog.LevelVar)
	levelVar.Set(slog.LevelDebug)
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: levelVar,
	})
	DaemonLogger.logger = slog.New(handler)

	// Test global functions
	Debugf("debug message %s", "test")
	Infof("info message %d", 42)
	Warnf("warn message %t", true)
	Errorf("error message %s %d", "test", 123)

	Debug("debug message")
	Info("info message")
	Warn("warn message")
	Error("error message")

	DebugKV("debug with kv", "key1", "value1")
	InfoKV("info with kv", "key2", "value2")
	WarnKV("warn with kv", "key3", "value3")
	ErrorKV("error with kv", "key4", "value4")

	output := buf.String()

	// Verify all messages appear
	expectedMessages := []string{
		"debug message test",
		"info message 42",
		"warn message true",
		"error message test 123",
		"debug message",
		"info message",
		"warn message",
		"error message",
		"debug with kv",
		"info with kv",
		"warn with kv",
		"error with kv",
	}

	for _, msg := range expectedMessages {
		if !strings.Contains(output, msg) {
			t.Errorf("Expected output to contain '%s'", msg)
		}
	}
}

func TestGlobalFunctionsWithoutInit(t *testing.T) {
	// Save original state
	originalDefaultLogger := DaemonLogger
	defer func() {
		DaemonLogger = originalDefaultLogger
	}()

	// Reset to uninitialized state
	DaemonLogger = nil

	// Test SetLevel without initialization
	err := SetLevel("debug")
	if err == nil {
		t.Error("Expected SetLevel to fail when logger not initialized")
	}

	// Test GetLevel without initialization
	level := GetLevel()
	if level != "info" {
		t.Errorf("Expected GetLevel to return info when not initialized, got %v", level)
	}

	// Test that global functions don't panic when logger is nil
	// These should not panic, but also won't produce output
	Debugf("debug message %s", "test")
	Infof("info message %d", 42)
	Warnf("warn message %t", true)
	Errorf("error message %s %d", "test", 123)

	Debug("debug message")
	Info("info message")
	Warn("warn message")
	Error("error message")

	DebugKV("debug with kv", "key1", "value1")
	InfoKV("info with kv", "key2", "value2")
	WarnKV("warn with kv", "key3", "value3")
	ErrorKV("error with kv", "key4", "value4")
}

func TestInvalidConfigFile(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "invalid-config.json")

	// Write invalid JSON
	err := os.WriteFile(configPath, []byte("invalid json"), 0644)
	if err != nil {
		t.Fatalf("Failed to write invalid config: %v", err)
	}

	// Should fail to create logger
	_, err = NewLogger(configPath)
	if err == nil {
		t.Error("Expected NewLogger to fail with invalid config file")
	}
}

func TestConfigWithInvalidLevel(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "invalid-level-config.json")

	// Write config with invalid level
	config := LogConfig{Level: "invalid"}
	data, _ := json.Marshal(config)
	err := os.WriteFile(configPath, data, 0644)
	if err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}

	// Should fail to create logger
	_, err = NewLogger(configPath)
	if err == nil {
		t.Error("Expected NewLogger to fail with invalid level in config")
	}
}

func TestParseLegacyLevels(t *testing.T) {
	tests := []struct {
		input    string
		expected int
		hasError bool
	}{
		{"trace", 6, false},
		{"debug", 5, false},
		{"info", 4, false},
		{"warning", 3, false},
		{"error", 2, false},
		{"fatal", 1, false},
		{"panic", 0, false},
		{"6", 6, false},
		{"5", 5, false},
		{"4", 4, false},
		{"3", 3, false},
		{"2", 2, false},
		{"1", 1, false},
		{"0", 0, false},
		{"invalid", 4, true}, // Should return default 4 with error
		{"", 4, true},        // Empty string should error
		{"unknown", 4, true}, // Unknown level should error
	}

	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			result, err := ParseLegacyLevels(test.input)

			if test.hasError {
				if err == nil {
					t.Errorf("Expected error for input '%s', but got none", test.input)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error for input '%s': %v", test.input, err)
				}
			}

			if result != test.expected {
				t.Errorf("For input '%s', expected %d, got %d", test.input, test.expected, result)
			}
		})
	}
}

func TestLevelToString(t *testing.T) {
	tests := []struct {
		input    slog.Level
		expected string
		hasError bool
	}{
		{slog.Level(-8), "trace", false},
		{slog.LevelDebug, "debug", false},
		{slog.LevelInfo, "info", false},
		{slog.LevelWarn, "warn", false},
		{slog.LevelError, "error", false},
		{slog.Level(16), "fatal", false},
		{slog.Level(32), "panic", false},
		{slog.Level(999), "info", true}, // Unknown level should return "info" with error
	}

	for _, test := range tests {
		t.Run(test.expected, func(t *testing.T) {
			result, err := LevelToString(test.input)

			if test.hasError {
				if err == nil {
					t.Errorf("Expected error for level %d, but got none", test.input)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error for level %d: %v", test.input, err)
				}
			}

			if result != test.expected {
				t.Errorf("For level %d, expected '%s', got '%s'", test.input, test.expected, result)
			}
		})
	}
}

func TestStringToLevel(t *testing.T) {
	tests := []struct {
		input    string
		expected slog.Level
		hasError bool
	}{
		{"trace", slog.Level(-8), false},
		{"debug", slog.LevelDebug, false},
		{"info", slog.LevelInfo, false},
		{"warn", slog.LevelWarn, false},
		{"error", slog.LevelError, false},
		{"fatal", slog.Level(16), false},
		{"panic", slog.Level(32), false},
		{"invalid", slog.LevelInfo, true}, // Should return LevelInfo with error
		{"", slog.LevelInfo, true},        // Empty string should error
		{"unknown", slog.LevelInfo, true}, // Unknown level should error
	}

	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			result, err := StringToLevel(test.input)

			if test.hasError {
				if err == nil {
					t.Errorf("Expected error for input '%s', but got none", test.input)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error for input '%s': %v", test.input, err)
				}
			}

			if result != test.expected {
				t.Errorf("For input '%s', expected %d, got %d", test.input, test.expected, result)
			}
		})
	}
}

func TestSetLevelWithoutConfigPath(t *testing.T) {
	// Create logger without config path
	logger, err := NewLogger("")
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}

	// SetLevel should work but not try to save config
	err = logger.SetLevel("debug")
	if err != nil {
		t.Errorf("SetLevel should work without config path, got error: %v", err)
	}

	if logger.GetLevel() != "debug" {
		t.Errorf("Expected level to be Debug, got %v", logger.GetLevel())
	}
}
