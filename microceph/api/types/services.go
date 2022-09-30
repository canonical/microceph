// Package types provides shared types and structs.
package types

type Services []Service
type Service struct {
	Service  string `json:"service" yaml:"service"`
	Location string `json:"location" yaml:"location"`
}
