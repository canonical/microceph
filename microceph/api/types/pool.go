package types

// Types for pool management.
type PoolPut struct {
	Pools []string `json:"pools" yaml:"pools"`
	Size  int64    `json:"size" yaml:"size"`
}
