package main

import (
	"context"
	"encoding/json"
	"os/exec"
	"time"
)

type CephConf interface {
	Load() error
	Get(string) (string, bool)
}

type Conf struct {
	rawJSON *[]byte
	data    map[string]string
}

// Retrieve configuration known to Ceph as raw JSON by executing the
// `ceph-conf` tool. A timeout is used to ensure execution does not block.
func (c *Conf) retrieveRawJSON() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ceph-conf", "-D", "--format", "json")

	output, err := cmd.Output()
	if ctx.Err() != nil {
		return ctx.Err()
	} else if err != nil {
		return err
	}
	c.rawJSON = &output
	return nil
}

// Retrieve and unmarshall JSON representation of Ceph configuration and store
// in a map in the received struct.
func (c *Conf) Load() error {
	var err error

	err = c.retrieveRawJSON()
	if err != nil {
		return err
	}
	err = json.Unmarshal(*c.rawJSON, &c.data)
	if err != nil {
		return err
	}
	return nil
}

// Look up key in map, return value or empty string and status of existence.
func (c *Conf) Get(key string) (string, bool) {
	v, ok := c.data[key]
	return v, ok
}
