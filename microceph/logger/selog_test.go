// Copyright 2026 Canonical Ltd
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package logger

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

func TestLogSuccess(t *testing.T) {
	var loggedMsg string
	oldCallback := RegisterLogCallback(func(msg string) {
		loggedMsg = msg
	})
	defer RegisterLogCallback(oldCallback)

	params := LogParams{
		Level:  "info",
		Msg:    "test msg",
		AppID:  "test_app",
		Event:  "sys_test",
		Detail: "test detail",
	}

	err := Log("test description", params)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if loggedMsg == "" {
		t.Fatal("Expected log message to be captured, but got empty string")
	}

	var parsed securityLog
	err = json.Unmarshal([]byte(loggedMsg), &parsed)
	if err != nil {
		t.Fatalf("Failed to parse logged message as JSON: %v", err)
	}

	if parsed.Level != "INFO" {
		t.Errorf("Expected Level to be 'INFO', got '%s'", parsed.Level)
	}
	if parsed.Msg != "test msg" {
		t.Errorf("Expected Msg to be 'test msg', got '%s'", parsed.Msg)
	}
	if parsed.Type != "security" {
		t.Errorf("Expected Type to be 'security', got '%s'", parsed.Type)
	}
	if parsed.AppID != "test_app" {
		t.Errorf("Expected AppID to be 'test_app', got '%s'", parsed.AppID)
	}
	if parsed.Event != "sys_test" {
		t.Errorf("Expected Event to be 'sys_test', got '%s'", parsed.Event)
	}
	if parsed.Detail != "test detail" {
		t.Errorf("Expected Detail to be 'test detail', got '%s'", parsed.Detail)
	}
	if parsed.Description != "test description" {
		t.Errorf("Expected Description to be 'test description', got '%s'", parsed.Description)
	}
	if parsed.Datetime == "" {
		t.Error("Expected Datetime to be non-empty")
	}
}

func TestLogInvalidEvent(t *testing.T) {
	params := LogParams{
		Event: "invalid_event",
	}
	err := Log("test description", params)
	if err == nil {
		t.Fatal("Expected error due to invalid event prefix, but got nil")
	}
	if !strings.Contains(err.Error(), "event must start with") {
		t.Errorf("Expected error message to contain 'event must start with', got '%v'", err)
	}
}

func TestLogInvalidLevel(t *testing.T) {
	params := LogParams{
		Level: "DEBUG",
		Event: "sys_test",
	}
	err := Log("test description", params)
	if err == nil {
		t.Fatal("Expected error due to invalid level, but got nil")
	}
	if !strings.Contains(err.Error(), "level must be one of") {
		t.Errorf("Expected error message to contain 'level must be one of', got '%v'", err)
	}
}

func TestLogDefaults(t *testing.T) {
	var loggedMsg string
	oldCallback := RegisterLogCallback(func(msg string) {
		loggedMsg = msg
	})
	defer RegisterLogCallback(oldCallback)

	oldDefaults := RegisterDefaults(map[string]string{
		"appid":  "default_appid",
		"detail": "default_detail",
	})
	defer RegisterDefaults(oldDefaults)

	params := LogParams{
		Event: "authn_login",
	}

	err := Log("test desc", params)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	var parsed securityLog
	err = json.Unmarshal([]byte(loggedMsg), &parsed)
	if err != nil {
		t.Fatalf("Failed to parse logged message as JSON: %v", err)
	}

	if parsed.AppID != "default_appid" {
		t.Errorf("Expected AppID to default to 'default_appid', got '%s'", parsed.AppID)
	}
	if parsed.Detail != "default_detail" {
		t.Errorf("Expected Detail to default to 'default_detail', got '%s'", parsed.Detail)
	}
	if parsed.Msg != "test desc" {
		t.Errorf("Expected Msg to default to description 'test desc', got '%s'", parsed.Msg)
	}
}

func TestLogConcurrent(t *testing.T) {
	// Restore defaults and callback after test
	oldCallback := RegisterLogCallback(defaultLogOutput)
	defer RegisterLogCallback(oldCallback)

	oldDefaults := RegisterDefaults(nil)
	defer RegisterDefaults(oldDefaults)

	const goroutines = 10
	const iterations = 50

	var wg sync.WaitGroup
	wg.Add(goroutines * 3)

	// Goroutines registering callbacks
	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				_ = RegisterLogCallback(func(msg string) {
					// Dummy callback
				})
			}
		}(i)
	}

	// Goroutines registering defaults
	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				_ = RegisterDefaults(map[string]string{
					"appid":  "app_id_concurrent",
					"detail": "detail_concurrent",
				})
			}
		}(i)
	}

	// Goroutines calling Log
	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				params := LogParams{
					Event: "sys_test",
				}
				_ = Log("concurrent log test", params)
			}
		}(i)
	}

	wg.Wait()
}
