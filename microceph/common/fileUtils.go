package common

import (
	"os"
	"strings"
	"time"

	"github.com/canonical/lxd/shared/logger"
	"github.com/djherbis/times"
)

// FilterFilesInDir filters filenames which matches the substring in path.
func FilterFilesInDir(subString string, path string) []string {
	files, err := os.ReadDir(path)
	if err != nil {
		return []string{}
	}

	resp := make([]string, len(files))
	for _, file := range files {
		if strings.Contains(file.Name(), subString) {
			resp = append(resp, file.Name())
		}
	}

	return resp
}

// GetFileAge fetches provided file's age in seconds; 0 in case of errors.
func GetFileAge(path string) float64 {
	t, err := times.Stat(path)
	if err != nil {
		logger.Error(err.Error())
		return 0
	}

	if !t.HasBirthTime() {
		return 0
	}

	// age is current time - birth time.
	return time.Since(t.BirthTime()).Seconds()
}
