// Package types provides shared types and structs.
package types

type MaintenanceResult struct {
	Name   string `json:"name"`
	Error  string `json:"error"`
	Action string `json:"action"`
}

type MaintenanceResults []MaintenanceResult

// Options for bringing a node into or out of maintenance
type CommonMaintenanceFlags struct {
	DryRun      bool `json:"dry_run"`
	CheckOnly   bool `json:"check_only"`
	IgnoreCheck bool `json:"ignore_check"`
}

// Options for bringing a node into maintenance
type EnterMaintenanceFlags struct {
	Force    bool `json:"force"`
	SetNoout bool `json:"set_noout"`
	StopOsds bool `json:"stop_osds"`
}

// MaintenanceRequest holds data structure for bringing a node into or out of maintenance
type MaintenanceRequest struct {
	Status string `json:"status" yaml:"status"`
	CommonMaintenanceFlags
	EnterMaintenanceFlags
}
