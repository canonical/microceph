package types

// ClientConfig type holds parameters from the `client config` API request
type ClientConfig struct {
	Key   string `json:"key" yaml:"key"`
	Value string `json:"value" yaml:"value"`
	Host  string `json:"host" yaml:"host"`
	Wait  bool   `json:"wait" yaml:"wait"`
}

// ClientConfigs is a slice of client configs
type ClientConfigs []ClientConfig
