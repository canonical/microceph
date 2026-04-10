// Package common package contains abstractions used by multiple other packages.
package common

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/canonical/microceph/microceph/logger"
)

// JoinTokenPeers returns the cluster member addresses embedded in a
// microcluster join token (base64-encoded JSON).
func JoinTokenPeers(token string) ([]string, error) {
	raw, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return nil, fmt.Errorf("decode token: %w", err)
	}

	var payload struct {
		JoinAddresses []string `json:"join_addresses"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("parse token: %w", err)
	}

	if len(payload.JoinAddresses) == 0 {
		return nil, fmt.Errorf("token contains no join addresses")
	}
	return payload.JoinAddresses, nil
}

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
