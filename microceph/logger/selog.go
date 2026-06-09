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
	"fmt"
	"strings"
	"sync"
	"time"
)

// Parameters for generating a security log.
type LogParams struct {
	Level  string
	Msg    string
	AppID  string
	Event  string
	Detail string
}

type LogCallback func(msg string)

var (
	// Overridable callback that outputs a log string.
	logCallbackFunc LogCallback = defaultLogOutput
	defaults = make(map[string]string)
	selogMtx sync.Mutex
)

func defaultLogOutput(msg string) {
	// For SEL, the allowed log levels are INFO, WARN and ERROR.
	// We pick WARN as the default, since INFO is too lenient and ERROR
	// is too strict.
	Warn(msg)
}

func RegisterLogCallback(fn LogCallback) LogCallback {
	selogMtx.Lock()
	defer selogMtx.Unlock()
	prev := logCallbackFunc
	logCallbackFunc = fn
	return prev
}

func RegisterDefaults(dfls map[string]string) map[string]string {
	selogMtx.Lock()
	defer selogMtx.Unlock()
	prev := defaults
	defaults = dfls
	return prev
}

// securityLog represents the JSON structure of a security event.
type securityLog struct {
	Level       string `json:"level"`
	Msg         string `json:"msg"`
	Type        string `json:"type"`
	Datetime    string `json:"datetime"`
	AppID       string `json:"appid"`
	Event       string `json:"event"`
	Detail      string `json:"detail"`
	Description string `json:"description"`
}

func makeLogStr(description, level, msg, appID, event, detail string) (string, error) {
	if !strings.HasPrefix(event, "sys_") &&
		!strings.HasPrefix(event, "authn_") &&
		!strings.HasPrefix(event, "authz_") {
		return "", fmt.Errorf("event must start with one of sys, authn or authz")
	}

	levelUpper := strings.ToUpper(level)
	if levelUpper != "INFO" && levelUpper != "WARN" && levelUpper != "ERROR" {
		return "", fmt.Errorf("level must be one of INFO, WARN, ERROR")
	}

	nowUTC := time.Now().UTC().Format("2006-01-02T15:04:05.000000-07:00")
	obj := securityLog{
		Level:       levelUpper,
		Msg:         msg,
		Type:        "security",
		Datetime:    nowUTC,
		AppID:       appID,
		Event:       event,
		Detail:      detail,
		Description: description,
	}

	data, err := json.Marshal(obj)
	if err != nil {
		return "", fmt.Errorf("failed to marshal security log: %w", err)
	}

	return string(data), nil
}

func mapDefault(val string, key string) string {
	if val != "" {
		return val
	}

	ret, ok := defaults[key]
	if ok {
		return ret
	}

	return val
}

func Log(description string, params LogParams) error {
	level := params.Level
	if level == "" {
		level = "WARN"
	}

	msg := params.Msg
	if msg == "" {
		msg = description
	}

	msg = mapDefault(msg, "msg")
	detail := mapDefault(params.Detail, "detail")
	appID := mapDefault(params.AppID, "appid")
	event := mapDefault(params.Event, "event")

	logStr, err := makeLogStr(description, level, msg, appID, event, detail)
	if err != nil {
		return err
	}

	cb := logCallbackFunc
	if cb != nil {
		cb(logStr)
	}

	return nil
}
