package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

type CephVolume interface {
	Load() error
}

type volume struct {
	rawJSON *[]byte
	data    map[string][]map[string][]map[string]string
}

// Retrieve LVM Logical Volume tags in raw JSON format by executing the `lvs`
// tool. A timeout is used to ensure execution does not block.
func (v *volume) retrieveRawJSON() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(
		ctx,
		"lvs",
		"--readonly",
		"--reportformat",
		"json",
		"-o",
		"lv_tags")

	output, err := cmd.Output()
	if ctx.Err() != nil {
		return ctx.Err()
	} else if err != nil {
		return err
	}
	v.rawJSON = &output
	return nil
}

// Retrieve and unmarshall JSON representation of LVM Logical Volume tags,
// extract LVs used for Ceph and store relevant information in a structured way
func (v *volume) Load() error {
	var err error

	err = v.retrieveRawJSON()
	if err != nil {
		return err
	}
	err = json.Unmarshal(*v.rawJSON, &v.data)
	if err != nil {
		return err
	}
	// TODO create struct and extract relevant data into it
	fmt.Println(v.data)
	return nil
}
