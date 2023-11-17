// Package types provides shared types and structs.
package types

// Services holds a slice of services
type Services []Service

// Service consist of a name and location
type Service struct {
	Service  string `json:"service" yaml:"service"`
	Location string `json:"location" yaml:"location"`
}

// Name: Name of the service to be enabled
// Wait: Whether the operation is to be performed in sync or async
// Payload: Service specific additional data encoded as a json string.
type EnableService struct {
	Name    string `json:"name" yaml:"name"`
	Wait    bool   `json:"bool" yaml:"bool"`
	Payload string `json:"payload" yaml:"payload"`
	// Enable Service passes all additional data as a json payload string.
}

// RGWService holds a port number and enable/disable flag
type RGWService struct {
	Service
	Port    int  `json:"port" yaml:"port"`
	Enabled bool `json:"enabled" yaml:"enabled"`
}

// Bootstrap holds the parameters required for bootstrapping the ceph cluster.
type Bootstrap struct {
	MonIp      string `json:"monip" yaml:"monip"`
	PublicNet  string `json:"public-net" yaml:"public-net"`
	ClusterNet string `json:"cluster-net" yaml:"cluster-net"`
}
