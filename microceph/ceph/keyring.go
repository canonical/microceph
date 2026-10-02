package ceph

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/canonical/microceph/microceph/common"
)

func genKeyring(path, name string, caps ...[]string) error {
	args := []string{
		"--create-keyring",
		path,
		"--gen-key",
		"-n", name,
	}

	for _, capability := range caps {
		if len(capability) != 2 {
			return fmt.Errorf("Invalid keyring capability: %v", capability)
		}

		args = append(args, "--cap", capability[0], capability[1])
	}

	_, err := common.ProcessExec.RunCommand("ceph-authtool", args...)
	if err != nil {
		return err
	}

	return nil
}

func importKeyring(path string, source string) error {
	args := []string{
		path,
		"--import-keyring",
		source,
	}

	_, err := common.ProcessExec.RunCommand("ceph-authtool", args...)
	if err != nil {
		return err
	}

	return nil
}

func genAuth(path string, name string, caps ...[]string) error {
	args := []string{
		"auth",
		"get-or-create",
		name,
	}

	for _, capability := range caps {
		if len(capability) != 2 {
			return fmt.Errorf("Invalid keyring capability: %v", capability)
		}

		args = append(args, capability[0], capability[1])
	}

	args = append(args, "-o", path)

	_, err := cephRun(args...)
	if err != nil {
		return err
	}

	return nil
}

func ParseKeyring(path string) (string, error) {
	// Open the CEPH keyring.
	cephKeyring, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("Failed to open %q: %w", path, err)
	}

	// Locate the keyring entry and its value.
	var cephSecret string
	scan := bufio.NewScanner(cephKeyring)
	for scan.Scan() {
		line := scan.Text()
		line = strings.TrimSpace(line)

		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "key") {
			fields := strings.SplitN(line, "=", 2)
			if len(fields) < 2 {
				continue
			}

			cephSecret = strings.TrimSpace(fields[1])
			break
		}
	}

	if cephSecret == "" {
		return "", fmt.Errorf("couldn't find a keyring entry")
	}

	return cephSecret, nil
}

// CreateClientKey creates a client key and returns the said key hash without saving it as a file.
// The key is read from the JSON that "ceph auth get-or-create" prints, and the call runs bound to ctx
// so that the ceph process is stopped when the caller gives up.
func CreateClientKey(ctx context.Context, clientName string, caps ...[]string) (string, error) {
	entity := fmt.Sprintf("client.%s", clientName)
	args := []string{
		"auth",
		"get-or-create",
		entity,
	}

	// add caps to the key.
	for _, capability := range caps {
		if len(capability) != 2 {
			return "", fmt.Errorf("invalid keyring capability: %v", capability)
		}

		args = append(args, capability[0], capability[1])
	}

	args = append(args, "--format", "json")

	output, err := cephRunContext(ctx, args...)
	if err != nil {
		return "", err
	}

	// A get-or-create that overlaps the creation of the same client replies once that creation
	// has committed, but prints nothing. The client exists by then, so asking again returns its key.
	if strings.TrimSpace(output) == "" {
		output, err = cephRunContext(ctx, args...)
		if err != nil {
			return "", err
		}
	}

	return parseClientKey(output, entity)
}

// parseClientKey returns the key of entity from the JSON array printed by "ceph auth get-or-create --format json".
func parseClientKey(output string, entity string) (string, error) {
	var entries []struct {
		Entity string `json:"entity"`
		Key    string `json:"key"`
	}

	err := json.Unmarshal([]byte(output), &entries)
	if err != nil {
		return "", fmt.Errorf("failed to parse the ceph auth output for %s: %w", entity, err)
	}

	for _, entry := range entries {
		if entry.Entity != entity {
			continue
		}

		if entry.Key == "" {
			return "", fmt.Errorf("ceph auth returned an empty key for %s", entity)
		}

		return entry.Key, nil
	}

	return "", fmt.Errorf("ceph auth returned no entry for %s", entity)
}

func DeleteClientKey(clientName string) error {
	args := []string{
		"auth",
		"del",
		fmt.Sprintf("client.%s", clientName),
	}

	_, err := cephRun(args...)
	if err != nil {
		return err
	}

	return nil
}
