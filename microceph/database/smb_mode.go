package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// GetSMBPlacementMode returns the mode in the last recorded upstream spec.
// Nil means no spec has been recorded; missing local files do not define a mode.
func GetSMBPlacementMode(ctx context.Context, tx *sql.Tx, groupID string) (*bool, error) {
	var config string
	err := tx.QueryRowContext(ctx, `SELECT coalesce(config, '{}') FROM service_groups WHERE service='smb' AND group_id=?`, groupID).Scan(&config)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var group SMBServiceGroupConfig
	err = json.Unmarshal([]byte(config), &group)
	if err != nil {
		return nil, fmt.Errorf("invalid stored SMB group configuration")
	}
	if len(group.DesiredSpec) == 0 || string(group.DesiredSpec) == "null" {
		return nil, nil
	}
	var spec struct {
		Features []string `json:"features"`
		Spec     *struct {
			Features []string `json:"features"`
		} `json:"spec"`
	}
	err = json.Unmarshal(group.DesiredSpec, &spec)
	if err != nil {
		return nil, fmt.Errorf("invalid stored SMB spec")
	}
	features := spec.Features
	if spec.Spec != nil {
		features = spec.Spec.Features
	}
	clustered := false
	for _, feature := range features {
		if feature == "clustered" {
			clustered = true
		}
	}
	return &clustered, nil
}

// CheckSMBPlacementMode rejects changing a group's recorded clustering mode or
// placing an additional member of a non-clustered group. It is a preflight check;
// it does not serialize direct upstream mutations with managed requests.
func CheckSMBPlacementMode(ctx context.Context, tx *sql.Tx, groupID, member string, clustered bool) error {
	mode, err := GetSMBPlacementMode(ctx, tx, groupID)
	if err != nil {
		return err
	}
	if mode != nil && *mode != clustered {
		return fmt.Errorf("SMB clustering mode cannot be changed; recreate the cluster")
	}
	if !clustered {
		var others int
		err = tx.QueryRowContext(ctx, `
SELECT count(*) FROM grouped_services g
 JOIN service_groups s ON s.id=g.service_group_id
 JOIN core_cluster_members m ON m.id=g.member_id
 WHERE s.service='smb' AND s.group_id=? AND m.name<>?`, groupID, member).Scan(&others)
		if err != nil {
			return err
		}
		if others > 0 {
			return fmt.Errorf("non-clustered SMB supports only one member")
		}
	}
	return nil
}
