package common

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/canonical/lxd/shared/logger"
	"github.com/djherbis/times"
)

// FilterFilesInDir filters filenames which matches the substring in path.
func FilterFilesInDir(subString string, path string) []string {
	files, err := filepath.Glob(path + fmt.Sprintf("*%s*", subString))
	if err != nil {
		logger.Errorf("failure finding files {%s} at path {%s}", subString, path)
		return []string{}
	}

	return files
}

// GetFileAge fetches provided file's age in seconds; in case of errors, 0 (zero) age is
// reported and errors are logged. This is because it is expected to be called for files
// which may not be present.
func GetFileAge(path string) float64 {
	t, err := times.Stat(path)
	if err != nil {
		logger.Error(err.Error())
		return 0
	}

	if !t.HasBirthTime() {
		logger.Warnf("File %s has no birth time.", path)
		return 0
	}

	// age is current time - birth time.
	return time.Since(t.BirthTime()).Seconds()
}
