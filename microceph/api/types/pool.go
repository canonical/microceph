package types

// Types for pool management.
type PoolPut struct {
	Pools []string `json:"pools" yaml:"pools"`
	Size  int64    `json:"size" yaml:"size"`
	// SetDefault controls whether osd_pool_default_size is also set to Size.
	// It is a pointer so that a request without the field, as sent by clients
	// that predate it, keeps setting the default.
	SetDefault *bool `json:"set_default,omitempty" yaml:"set_default,omitempty"`
}

// WantsDefault reports whether the request also sets the default pool size.
func (p PoolPut) WantsDefault() bool {
	return p.SetDefault == nil || *p.SetDefault
}

// Pool represents information about an OSD pool.
type Pool struct {
	Pool      string `json:"pool" yaml:"pool"`
	PoolID    int64  `json:"pool_id" yaml:"pool_id"`
	Size      int64  `json:"size" yaml:"size"`
	MinSize   int64  `json:"min_size" yaml:"min_size"`
	CrushRule string `json:"crush_rule" yaml:"crush_rule"`
}
