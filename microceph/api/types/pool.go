package types

// Types for pool management.
type PoolPut struct {
	Pools []string `json:"pools" yaml:"pools"`
	Size  int64    `json:"size" yaml:"size"`
}

// Pool represents information about an OSD pool.
type Pool struct {
	Pool      string `json:"pool" yaml:"pool"`
	PoolID    int64  `json:"pool_id" yaml:"pool_id"`
	Size      int64  `json:"size" yaml:"size"`
	MinSize   int64  `json:"min_size" yaml:"min_size"`
	CrushRule string `json:"crush_rule" yaml:"crush_rule"`
}
