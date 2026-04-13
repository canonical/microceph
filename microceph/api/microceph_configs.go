package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/logger"
	mcTypes "github.com/canonical/microcluster/v3/microcluster/types"
)

// top level microceph API
var microcephCmd = mcTypes.Endpoint{
	Path: "microceph",
}

// microceph configs API
var microcephConfigsCmd = mcTypes.Endpoint{
	Path: "microceph/configs",
}

var logLevelCmd = mcTypes.Endpoint{
	Path: "microceph/configs/log-level",
	Put:  mcTypes.EndpointAction{Handler: logLevelPut, ProxyTarget: true},
	Get:  mcTypes.EndpointAction{Handler: logLevelGet, ProxyTarget: true},
}

func logLevelPut(s mcTypes.State, r *http.Request) mcTypes.Response {
	var req types.LogLevelPut

	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return mcTypes.InternalError(err)
	}

	logger.Debugf("cmdLogLevelPut: %v", req)

	ls := strings.ToLower(req.Level)
	i, err := logger.ParseLegacyLevels(ls) // validate
	if err != nil {
		return mcTypes.BadRequest(err)
	}

	err = logger.SetLevel(logger.ParseLegacyLevelsInt(i))
	if err != nil {
		return mcTypes.SmartError(err)
	}

	logger.Debugf("cmdLogLevelPut done: %v", req)
	return mcTypes.EmptySyncResponse
}

func logLevelGet(s mcTypes.State, r *http.Request) mcTypes.Response {
	currentLevel := logger.GetLevel()
	i, err := logger.ParseLegacyLevels(currentLevel)
	if err != nil {
		logger.Errorf("cmdLogLevelGet: failed to parse current log level %q: %v", currentLevel, err)
		return mcTypes.InternalError(err)
	}
	return mcTypes.SyncResponse(true, i)
}
