// Package common package contains abstractions used by multiple other packages.
package common

import (
	"github.com/canonical/microceph/microceph/logger"
)

// JoinConfig holds all additional parameters that could be provided to the join API/CLI command.
type JoinConfig struct {
	AvailabilityZone string // Availability Zone of the host.
}

func EncodeJoinConfig(data JoinConfig) map[string]string {
	logger.Debugf("encoding join config: %+v", data)

	return map[string]string{
		"AvailabilityZone": data.AvailabilityZone,
	}
}

func DecodeJoinConfig(input map[string]string, data *JoinConfig) {
	logger.Debugf("decoding join config: %+v", input)

	data.AvailabilityZone = input["AvailabilityZone"]
}
