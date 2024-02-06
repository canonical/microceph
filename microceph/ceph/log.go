package ceph

import (
	"fmt"
	"strconv"
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

func SetLogLevel(level string) error {
	lrLevel, err := logrus.ParseLevel(level)
	if err != nil {
		// Has to be an integer level.
		ilvl, err := strconv.Atoi(level)
		if err != nil {
			return err
		} else if ilvl < 0 || ilvl > int(logrus.TraceLevel) {
			return fmt.Errorf("invalid log level: %u", ilvl)
		}

		lrLevel = logrus.Level(ilvl)
	}

	wrapper := (*logWrapper)(unsafe.Pointer(&logger.Log))
	target := (*logrus.Logger)(unsafe.Pointer(&wrapper.target))
	target.SetLevel(lrLevel)
	return nil
}

func GetLogLevel() uint32 {
	wrapper := (*logWrapper)(unsafe.Pointer(&logger.Log))
	target := (*logrus.Logger)(unsafe.Pointer(&wrapper.target))
	return uint32(target.GetLevel())
}
