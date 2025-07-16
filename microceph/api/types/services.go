// Package types provides shared types and structs.
package types

import (
	"regexp"
)

// Services holds a slice of services
type Services []Service

// Service consist of a name and location
type Service struct {
	Service  string `json:"service" yaml:"service"`
	Location string `json:"location" yaml:"location"`
	GroupID  string `json:"group_id" yaml:"group_id"`
	Info     string `json:"info" yaml:"info"`
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

// NFSService holds the Cluster ID of the NFS Service.
type NFSService struct {
	ClusterID string `json:"cluster_id" yaml:"cluster_id"`
}

// NFSClusterIDRegex is a regex for acceptable ClusterIDs.
var NFSClusterIDRegex = regexp.MustCompile(`^[\w][\w.-]{1,61}[\w]$`)

// RGWService holds a port number and enable/disable flag
type RGWService struct {
	Service
	Port    int  `json:"port" yaml:"port"`
	Enabled bool `json:"enabled" yaml:"enabled"`
}

// MonitorStatus holds the status of all monitors
// for now, this is just the addresses of the monitors
type MonitorStatus struct {
	Addresses []string `json:"addresses" yaml:"addresses"`
}
