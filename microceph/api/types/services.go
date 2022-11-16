// Package types provides shared types and structs.
package types

// Services holds a slice of services
type Services []Service

// Service consist of a name and location
type Service struct {
	Service  string `json:"service" yaml:"service"`
	Location string `json:"location" yaml:"location"`
}
