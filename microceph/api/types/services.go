// Package types provides shared types and structs.
package types

// Services holds a slice of services
type Services []Service

// Service consist of a name and location
type Service struct {
	Service  string `json:"service" yaml:"service"`
	Location string `json:"location" yaml:"location"`
}

// RGWService holds a port number and enable/disable flag
type RGWService struct {
	Service
	Port    int  `json:"port" yaml:"port"`
	Enabled bool `json:"enabled" yaml:"enabled"`
}
