package ceph

import (
	"fmt"
	"reflect"

	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/database"
)

// ClientConfigT holds all the client configuration values *applicable* for
// the host machine. These values are consumed by configwriter for ceph.conf
// updation. This approach keeps the client config updation logic tied together
// and easily extendable for more keys.
type ClientConfigT struct {
	IsCache             string
	CacheSize           string
	IsCacheWritethrough string
	CacheMaxDirty       string
	CacheTargetDirty    string
}

func GetClientConfigForHost(s common.StateInterface, hostname string) (ClientConfigT, error) {
	retval := ClientConfigT{}

	// Get all client configs for the current host.
	configs, err := database.ClientConfigQuery.GetAllForHost(s.ClusterState(), hostname)
	if err != nil {
		return ClientConfigT{}, fmt.Errorf("could not query database for client configs: %v", err)
	}

	logger.Infof("Client Configs for host %s, %v", hostname, configs)

	for _, config := range configs {
		// Populate client config table using the database values.
		setterTable := GetClientConfigSet()
		err = setFieldValue(&retval, fmt.Sprint(setterTable[config.Key]), config.Value)
		if err != nil {
			return ClientConfigT{}, fmt.Errorf("failed object population: %v", err)
		}
	}

	return retval, nil
}

func setFieldValue(ogp *ClientConfigT, field string, value string) error {
	r := reflect.ValueOf(ogp)
	f := reflect.Indirect(r).FieldByName(field)
	if f.Kind() != reflect.Invalid {
		f.SetString(value)
		return nil
	}
	return fmt.Errorf("cannot set field %s", field)
}

func GetClientConfigSet() Set {
	return Set{
		"rbd_cache":                          "IsCache",
		"rbd_cache_size":                     "CacheSize",
		"rbd_cache_writethrough_until_flush": "IsCacheWritethrough",
		"rbd_cache_max_dirty":                "CacheMaxDirty",
		"rbd_cache_target_dirty":             "CacheTargetDirty",
	}
}
