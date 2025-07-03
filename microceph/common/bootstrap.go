package common

import (
    "strconv"
)

type BootstrapConfig struct {
	MonIp      string
	PublicNet  string
	ClusterNet string
	V2Only     bool
}

func EncodeBootstrapConfig(data BootstrapConfig) map[string]string {
	return map[string]string{
		"MonIp":      data.MonIp,
		"PublicNet":  data.PublicNet,
		"ClusterNet": data.ClusterNet,
		"V2Only":     strconv.FormatBool(data.V2Only),
	}
}

func DecodeBootstrapConfig(input map[string]string, data *BootstrapConfig) {
	data.MonIp = input["MonIp"]
	data.PublicNet = input["PublicNet"]
	data.ClusterNet = input["ClusterNet"]
	data.V2Only, _ = strconv.ParseBool(input["V2Only"])
}
