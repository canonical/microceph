package types

// AuthRotateRequest holds parameters for rotating CephX authentication keys.
type AuthRotateRequest struct {
	KeyType string `json:"key_type" yaml:"key_type"`
	Client  string `json:"client" yaml:"client"`
}

// AuthRotateResponse represents the outcome of an auth rotate command.
type AuthRotateResponse struct {
	TargetKeyType string `json:"target_key_type" yaml:"target_key_type"`
	State         string `json:"state" yaml:"state"`
	Stage         string `json:"stage" yaml:"stage"`
	ClientName    string `json:"client_name,omitempty" yaml:"client_name,omitempty"`
	Blocker       string `json:"blocker,omitempty" yaml:"blocker,omitempty"`
	Detail        string `json:"detail,omitempty" yaml:"detail,omitempty"`
}

// AuthStatusResponse represents the status and client cipher distribution of CephX authentication.
type AuthStatusResponse struct {
	Status             string              `json:"status" yaml:"status"`
	State              string              `json:"state" yaml:"state"`
	Blocker            string              `json:"blocker,omitempty" yaml:"blocker,omitempty"`
	TargetKeyType      string              `json:"target_key_type,omitempty" yaml:"target_key_type,omitempty"`
	ClientDistribution map[string][]string `json:"client_distribution,omitempty" yaml:"client_distribution,omitempty"`
	Detail             string              `json:"detail,omitempty" yaml:"detail,omitempty"`
}
