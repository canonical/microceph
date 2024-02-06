package ceph

import (
	"fmt"
	"strconv"
	"strings"
	"unsafe"

	"github.com/canonical/lxd/shared/logger"
	"github.com/sirupsen/logrus"
)

// The following is hack to access a private member in lxd's logger.

type targetLogger interface {
	Panic(args ...interface{})
	Fatal(args ...interface{})
	Error(args ...interface{})
	Warn(args ...interface{})
	Info(args ...interface{})
	Debug(args ...interface{})
	Trace(args ...interface{})
	WithFields(fields logrus.Fields) *logrus.Entry
}

type logWrapper struct {
	target targetLogger
}

func parseLogLevel(level string) (int, error) {
	level = strings.ToLower(level)

	if level == "panic" {
		return 0, nil
	} else if level == "fatal" {
		return 1, nil
	} else if level == "error" {
		return 2, nil
	} else if level == "warning" {
		return 3, nil
	} else if level == "info" {
		return 4, nil
	} else if level == "debug" {
		return 5, nil
	} else if level == "trace" {
		return 6, nil
	}

	return 0, fmt.Errorf("invalid log level: %s", level)
}

func SetLogLevel(level string) error {
	ilvl, err := strconv.Atoi(level)
	if err != nil {
		// Level is a symbolic string.
		ilvl, err = parseLogLevel(level)
		if err != nil {
			return err
		}
	} else if ilvl < 0 || ilvl > 6 {
		return fmt.Errorf("log level must be between 0 and 6")
	}

	wrapper := (*logWrapper)(unsafe.Pointer(&logger.Log))
	target := (*logrus.Logger)(unsafe.Pointer(&wrapper.target))
	target.SetLevel(logrus.Level(ilvl))
	return nil
}

func GetLogLevel() uint32 {
	wrapper := (*logWrapper)(unsafe.Pointer(&logger.Log))
	target := (*logrus.Logger)(unsafe.Pointer(&wrapper.target))
	return uint32(target.GetLevel())
}
