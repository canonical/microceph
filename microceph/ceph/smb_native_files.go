package ceph

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/canonical/microceph/microceph/common"
)

// Native operation seams belong to placement, not an executable router.
var compileSMBNativeFunc = compileSMBNative
var publishSMBNativeFunc = publishSMBNative
var smbReadyNativeFunc = smbReadyNative

// smbNativeFailure preserves detailed private diagnostics at direct-call and app
// return boundaries. Never wrap the private error into an API/log-facing error.
func smbNativeFailure(root string, retErr *error) {
	if *retErr != nil {
		_ = writeSMBNativeFile(filepath.Join(root, "diagnostics/native-error"), []byte((*retErr).Error()+"\n"))
		*retErr = fmt.Errorf("native SMB operation failed; inspect private cluster diagnostics")
	}
}

// smbPythonBinding keeps documents/credentials in private request files, never
// argv. Only pinned upstream operations cross this process boundary.
func smbPythonBinding(ctx context.Context, root string) smbBinding {
	return func(command string, request any, result any) error {
		switch command {
		case "inspect", "render", "accounts", "check-data":
		default:
			return fmt.Errorf("unsupported upstream binding operation")
		}
		directory := filepath.Join(root, "config")
		err := smbPrivateDir(directory)
		if err != nil {
			return err
		}
		file, err := os.CreateTemp(directory, ".native-input-*")
		if err != nil {
			return err
		}
		path := file.Name()
		defer os.Remove(path)
		err = file.Close()
		if err != nil {
			return err
		}
		data, err := json.Marshal(request)
		if err != nil {
			return err
		}
		err = writeSMBNativeFile(path, data)
		if err != nil {
			return err
		}
		snap := os.Getenv("SNAP")
		argv := []string{filepath.Join(snap, "bin/python3"), "-B", filepath.Join(snap, "smb/native.py"), command, path}
		var output string
		if command == "accounts" {
			var raw []byte
			raw, err = runSMBPrivateCommandFunc(ctx, root, argv, nil)
			output = string(raw)
		} else {
			output, err = common.ProcessExec.RunCommandContext(ctx, argv[0], argv[1:]...)
		}
		if err != nil {
			return fmt.Errorf("upstream %s binding failed: %w", command, err)
		}
		if len(output) > 32*1024*1024 || !json.Valid([]byte(output)) {
			return fmt.Errorf("invalid upstream binding response")
		}
		if result == nil {
			return nil
		}
		return json.Unmarshal([]byte(output), result)
	}
}

func compileSMBNative(ctx context.Context, root string, candidate smbCandidate) (retErr error) {
	err := smbNativeRoot(root, filepath.Base(root))
	if err != nil {
		return err
	}
	defer smbNativeFailure(root, &retErr)
	err = ctx.Err()
	if err != nil {
		return err
	}
	binding := smbPythonBinding(ctx, root)
	plan, err := projectSMBNative(candidate, root, binding)
	if err != nil {
		return err
	}
	next := filepath.Join(root, "config/next")
	err = smbPrivateDir(next)
	if err != nil {
		return err
	}
	// Only obsolete staged files are removed. Live state and shared data are not.
	directories := []string{}
	err = filepath.WalkDir(next, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("native candidate is an escaping symlink")
		}
		if entry.IsDir() {
			directories = append(directories, path)
			return nil
		}
		relative, err := filepath.Rel(next, path)
		if err != nil {
			return err
		}
		_, exists := plan.Files[relative]
		if !exists {
			return os.Remove(path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Sort(sort.Reverse(sort.StringSlice(directories)))
	for _, directory := range directories {
		if directory == next {
			continue
		}
		entries, err := os.ReadDir(directory)
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			err = os.Remove(directory)
			if err != nil {
				return err
			}
		}
	}
	names := make([]string, 0, len(plan.Files))
	for name := range plan.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !filepath.IsLocal(name) || filepath.Clean(name) != name {
			return fmt.Errorf("invalid staged native path")
		}
		err = writeSMBNativeFile(filepath.Join(next, name), plan.Files[name])
		if err != nil {
			return err
		}
	}
	_, err = common.ProcessExec.RunCommandContext(ctx, filepath.Join(os.Getenv("SNAP"), "bin/testparm"), "--suppress-prompt", filepath.Join(next, "config/smb.conf"))
	if err != nil {
		return fmt.Errorf("native Samba configuration validation failed: %w", err)
	}
	if len(plan.Launch.DataChecks) > 0 {
		request := smbBindingRequest{ConfigPath: filepath.Join(next, "config/ceph-direct.conf"), Checks: plan.Launch.DataChecks}
		err = binding("check-data", request, nil)
	}
	return err
}
