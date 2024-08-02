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
