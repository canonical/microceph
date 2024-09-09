package types

// RemoteImportRequest abstracts the data members for the remote import request.
type RemoteImportRequest struct {
	Name       string            `json:"name" yaml:"name"`
	LocalName  string            `json:"local_name" yaml:"local_name"`
	Config     map[string]string `json:"config" yaml:"config"`
	RenderOnly bool              `json:"render_only" yaml:"render_only"`
}

func (r *RemoteImportRequest) Init(localName string, remoteName string, renderOnly bool) *RemoteImportRequest {
	r.LocalName = localName
	r.Name = remoteName
	r.Config = make(map[string]string)
	r.RenderOnly = false
	return r
}

// ClusterExportRequest abstracts the data members for cluster export request.
type ClusterExportRequest struct {
	RemoteName string `json:"remote_name" yaml:"remote_name"`
}

// RemoteRecord exposes remote record structure in db to the client package.
type RemoteRecord struct {
	// NOTE (utkarshbhatthere): The member names for this data structure
	// should match the database record structure. This has been taken out
	// since the client package should not import from database package.
	ID int `json:"id" yaml:"id"`
	// remote cluster name
	Name string `json:"name" yaml:"name"`
	// local cluster name
	LocalName string `json:"local_name" yaml:"local_name"`
}
