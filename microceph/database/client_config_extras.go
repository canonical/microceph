package database

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/canonical/microceph/microceph/constants"

	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/microceph/microceph/logger"
	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microcluster/v2/cluster"
	"github.com/canonical/microcluster/v2/state"
)

var _ = api.ServerEnvironment{}

var globalClientConfigItemObjects = cluster.RegisterStmt(`
SELECT client_config.id, client_config.key, client_config.value FROM client_config
  WHERE client_config.member_id IS NULL
  ORDER BY client_config.key
`)

var globalClientConfigItemObjectByKey = cluster.RegisterStmt(`
SELECT client_config.id, client_config.key, client_config.value FROM client_config
  WHERE ( client_config.key = ? AND client_config.member_id IS NULL )
`)

var globalClientConfigItemCreateOrUpdate = cluster.RegisterStmt(`
INSERT OR REPLACE INTO client_config (member_id, key, value)
  VALUES (NULL, ?, ?)
`)

var clientConfigItemCreateOrUpdate = cluster.RegisterStmt(`
INSERT OR REPLACE INTO client_config (member_id, key, value)
  VALUES ((SELECT core_cluster_members.id FROM core_cluster_members WHERE core_cluster_members.name = ?), ?, ?)
`)

// Slice of ClientConfigItem(s)
type ClientConfigItems []ClientConfigItem

// GetClientConfigSlice translates a slice of ClientConfigItems (used in DB ops) to types.ClientConfigs (used in API ops)
func (cci ClientConfigItems) GetClientConfigSlice() types.ClientConfigs {
	var host string
	ccs := make(types.ClientConfigs, len(cci))
	for i, configItem := range cci {
		if len(configItem.Host) > 0 {
			host = configItem.Host
		} else {
			host = constants.ClientConfigGlobalHostConst
		}

		ccs[i] = types.ClientConfig{
			Key:   configItem.Key,
			Value: configItem.Value,
			Host:  host,
		}
	}

	return ccs
}

type ClientConfigQueryIntf interface {

	// Add Method
	AddNew(ctx context.Context, s state.State, key string, value string, host string) error

	// Fetch Methods
	GetAll(ctx context.Context, s state.State) (ClientConfigItems, error)
	GetAllForKey(ctx context.Context, s state.State, key string) (ClientConfigItems, error)
	GetAllForHost(ctx context.Context, s state.State, host string) (ClientConfigItems, error)
	GetAllForKeyAndHost(ctx context.Context, s state.State, key string, host string) (ClientConfigItems, error)

	// Delete Methods
	RemoveAllForKey(ctx context.Context, s state.State, key string) error
	RemoveOneForKeyAndHost(ctx context.Context, s state.State, key string, host string) error
}

type ClientConfigQueryImpl struct{}

// Add Method
func (ccq ClientConfigQueryImpl) AddNew(ctx context.Context, s state.State, key string, value string, host string) error {
	err := s.Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		data := ClientConfigItem{
			Key:   key,
			Value: value,
			Host:  host,
		}
		// Add record to database.
		err := createOrUpdateClientConfigItem(ctx, tx, data)
		if err != nil {
			return fmt.Errorf("failed to add client config: %v", err)
		}

		return nil
	})
	if err != nil {
		return err
	}
	return nil
}

// Fetch Methods
func (ccq ClientConfigQueryImpl) GetAll(ctx context.Context, s state.State) (ClientConfigItems, error) {
	globalConfigs, err := ccq.GetGlobalConfigs(ctx, s, "")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch global client configs: %w", err)
	}

	logger.Infof("Global Configs: %v", globalConfigs)

	hostConfigs, err := ccq.GetAllForFilter(ctx, s)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch host configured client configs: %w", err)
	}

	logger.Infof("Host Configs: %v", hostConfigs)

	return append(globalConfigs, hostConfigs...), nil
}

func (ccq ClientConfigQueryImpl) GetAllForKey(ctx context.Context, s state.State, key string) (ClientConfigItems, error) {
	globalConfigs, err := ccq.GetGlobalConfigs(ctx, s, key)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch global client configs, key %s: %w", key, err)
	}

	logger.Infof("Global Configs: %v", globalConfigs)

	hostConfigs, err := ccq.GetAllForFilter(ctx, s, ClientConfigItemFilter{Host: nil, Key: &key})
	if err != nil {
		return nil, fmt.Errorf("failed to fetch host configured client configs, key %s: %w", key, err)
	}

	logger.Infof("Host Configs: %v", hostConfigs)

	return append(globalConfigs, hostConfigs...), nil
}

func (ccq ClientConfigQueryImpl) GetAllForHost(ctx context.Context, s state.State, host string) (ClientConfigItems, error) {
	globalConfigs, err := ccq.GetGlobalConfigs(ctx, s, "")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch global client configs, host %s: %w", host, err)
	}

	logger.Infof("Global Configs: %v", globalConfigs)

	hostConfigs, err := ccq.GetAllForFilter(ctx, s, ClientConfigItemFilter{Host: &host, Key: nil})
	if err != nil {
		return nil, fmt.Errorf("failed to fetch host client configs, host %s: %w", host, err)
	}

	logger.Infof("Host Configs: %v", hostConfigs)

	return squashClientConfigs(globalConfigs, hostConfigs), nil
}

func (ccq ClientConfigQueryImpl) GetAllForKeyAndHost(ctx context.Context, s state.State, key string, host string) (ClientConfigItems, error) {
	return ccq.GetAllForFilter(ctx, s, ClientConfigItemFilter{Host: &host, Key: &key})
}

func (ccq ClientConfigQueryImpl) GetAllForFilter(ctx context.Context, s state.State, filters ...ClientConfigItemFilter) (ClientConfigItems, error) {
	var err error
	var retval []ClientConfigItem

	err = s.Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		retval, err = GetClientConfigItems(ctx, tx, filters...)
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return retval, err
	}
	return retval, nil
}

// Fetch client configs using registered sql stmt and args
func (ccq ClientConfigQueryImpl) GetGlobalConfigs(ctx context.Context, s state.State, key string) ([]ClientConfigItem, error) {
	var err error
	objects := make([]ClientConfigItem, 0)

	// scan handler for global configs.
	dest := func(scan func(dest ...any) error) error {
		c := ClientConfigItem{Host: constants.ClientConfigGlobalHostConst}
		err := scan(&c.ID, &c.Key, &c.Value)
		if err != nil {
			return err
		}

		objects = append(objects, c)

		logger.Infof("Object: %v, Objects: %v", c, objects)
		return nil
	}

	err = s.Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if len(key) != 0 {
			return getOneGlobalConfigByKey(ctx, tx, dest, key)
		}

		return getAllGlobalConfigs(ctx, tx, dest)
	})
	if err != nil {
		return nil, err
	}

	return objects, nil
}

// Delete Methods
func (ccq ClientConfigQueryImpl) RemoveAllForKey(ctx context.Context, s state.State, key string) error {
	err := s.Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		err := DeleteClientConfigItems(ctx, tx, key)
		if err != nil {
			return fmt.Errorf("failed to clean existing keys %s: %v", key, err)
		}

		return nil
	})
	if err != nil {
		return err
	}
	return nil
}

func (ccq ClientConfigQueryImpl) RemoveOneForKeyAndHost(ctx context.Context, s state.State, key string, host string) error {
	err := s.Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		err := DeleteClientConfigItem(ctx, tx, key, host)
		if err != nil {
			return fmt.Errorf("failed to clean existing keys %s: %v", key, err)
		}

		return nil
	})
	if err != nil {
		return err
	}
	return nil
}

/******************** HELPER FUNCTIONS ********************/
// createOrUpdateClientConfigItem adds a new ClientConfigItem to the database or updates the existing one if it exists.
func createOrUpdateClientConfigItem(_ context.Context, tx *sql.Tx, object ClientConfigItem) error {
	var stmtIndex int
	var args []any

	// Populate the statement arguments.
	if object.Host == constants.ClientConfigGlobalHostConst {
		args = append(args, object.Key)
		args = append(args, object.Value)
		stmtIndex = globalClientConfigItemCreateOrUpdate
	} else {
		args = append(args, object.Host)
		args = append(args, object.Key)
		args = append(args, object.Value)
		stmtIndex = clientConfigItemCreateOrUpdate
	}

	// Prepared statement to use.
	stmt, err := cluster.Stmt(tx, stmtIndex)
	if err != nil {
		return fmt.Errorf("failed to prepare statement for %v: %w", object, err)
	}

	// Execute the statement.
	_, err = stmt.Exec(args...)
	if err != nil {
		return fmt.Errorf("failed to insert %v: %w", object, err)
	}

	return nil
}

// Squash host configs over global configs to generate a slice of applicable configs for a host.
func squashClientConfigs(globalConfigs ClientConfigItems, hostConfigs ClientConfigItems) ClientConfigItems {
	configMap := map[string]ClientConfigItem{}

	// Populate global configs in the output configs object.
	for _, config := range globalConfigs {
		configMap[config.Key] = config
	}

	logger.Infof("Map post global key updation: %v", configMap)

	// Overwrite global configs if host level config exists.
	for _, config := range hostConfigs {
		configMap[config.Key] = config
	}

	logger.Infof("Map post host key updation: %v", configMap)

	configs := ClientConfigItems{}
	for _, item := range configMap {
		configs = append(configs, item)
	}

	logger.Infof("Squashed slice: %v", configs)

	return configs
}

// getAllGlobalConfigs performs sql query for all global configurations.
func getAllGlobalConfigs(ctx context.Context, tx *sql.Tx, rowFunc query.Dest) error {
	queryStr, err := cluster.StmtString(globalClientConfigItemObjects)
	if err != nil {
		return fmt.Errorf("failed to parse sql stmt table: %w", err)
	}

	logger.Infof("Query Str: %s", queryStr)

	err = query.Scan(ctx, tx, queryStr, rowFunc)
	if err != nil {
		return fmt.Errorf("failed to fetch from client_config table: %w", err)
	}

	return nil
}

// getOneGlobalConfigByKey performs sql query for a single global configuration using config key.
func getOneGlobalConfigByKey(ctx context.Context, tx *sql.Tx, rowFunc query.Dest, key string) error {
	queryStr, err := cluster.StmtString(globalClientConfigItemObjectByKey)
	if err != nil {
		return fmt.Errorf("failed to parse sql stmt table: %w", err)
	}

	logger.Infof("Query Str: %s", queryStr)

	err = query.Scan(ctx, tx, queryStr, rowFunc, key)
	if err != nil {
		return fmt.Errorf("failed to fetch from client_config table: %w", err)
	}

	return nil
}

// Singleton for mocker
var ClientConfigQuery ClientConfigQueryIntf = ClientConfigQueryImpl{}
