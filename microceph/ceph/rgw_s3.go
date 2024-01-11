package ceph

import (
	"encoding/json"
	"fmt"

	"github.com/canonical/microceph/microceph/api/types"
)

func CreateS3User(user types.S3User) (string, error) {
	args := []string{
		"user",
		"create",
		fmt.Sprintf("--uid=%s", user.Name),
		fmt.Sprintf("--display-name=%s", user.Name),
	}

	if len(user.Key) > 0 {
		args = append(args, fmt.Sprintf("--access-key=%s", user.Key))
	}

	if len(user.Secret) > 0 {
		args = append(args, fmt.Sprintf("--secret=%s", user.Secret))
	}

	output, err := processExec.RunCommand("radosgw-admin", args...)
	if err != nil {
		return "", err
	}

	return output, nil
}

func GetS3User(user types.S3User) (string, error) {
	args := []string{
		"user",
		"info",
		fmt.Sprintf("--uid=%s", user.Name),
	}

	output, err := processExec.RunCommand("radosgw-admin", args...)
	if err != nil {
		return "", err
	}

	return output, nil
}

func ListS3Users() ([]string, error) {
	args := []string{
		"user",
		"list",
	}

	output, err := processExec.RunCommand("radosgw-admin", args...)
	if err != nil {
		return []string{}, err
	}

	ret := []string{}
	json.Unmarshal([]byte(output), &ret)
	return ret, nil
}

func DeleteS3User(name string) error {
	args := []string{
		"user",
		"rm",
		fmt.Sprintf("--uid=%s", name),
	}

	_, err := processExec.RunCommand("radosgw-admin", args...)
	if err != nil {
		return err
	}

	return nil
}
