package types

type Remote struct {
	Name       string            `json:"name" yaml:"name"`
	LocalName  string            `json:"local_name" yaml:"local_name"`
	Config     map[string]string `json:"config" yaml:"config"`
	RenderOnly bool              `json:"render_only" yaml:"render_only"`
}

func (r *Remote) Init(localName string, remoteName string, renderOnly bool) *Remote {
	r.LocalName = localName
	r.Name = remoteName
	r.Config = make(map[string]string)
	r.RenderOnly = false
	return r
}

type ClusterStateRequest struct {
	RemoteName string `json:"remote_name" yaml:"remote_name"`
}

// NOTE (utkarshbhatthere): The member names for this data structure
// should match the database record structure. This has been taken out
// since the client package should not import from database package.
type RemoteRecord struct {
	ID        int
	Name      string // remote cluster name
	LocalName string // local cluster name
}
