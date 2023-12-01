package common

type BootstrapConfig struct {
	MonIp      string
	PublicNet  string
	ClusterNet string
}

func EncodeBootstrapConfig(data BootstrapConfig) map[string]string {
	return map[string]string{
		"MonIp":      data.MonIp,
		"PublicNet":  data.PublicNet,
		"ClusterNet": data.ClusterNet,
	}
}

func DecodeBootstrapConfig(input map[string]string, data *BootstrapConfig) {
	data.MonIp = input["MonIp"]
	data.PublicNet = input["PublicNet"]
	data.ClusterNet = input["ClusterNet"]
}
