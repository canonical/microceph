package ceph

import (
	"bufio"
	"fmt"
	"os"
	"strings"
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

	_, err := processExec.RunCommand("ceph-authtool", args...)
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

	_, err := processExec.RunCommand("ceph-authtool", args...)
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

func parseKeyring(path string) (string, error) {
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
		return "", fmt.Errorf("Couldn't find a keyring entry")
	}

	return cephSecret, nil
}
