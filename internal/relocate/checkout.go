package relocate

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
)

// fileState includes raw VC metadata. Read-only validation must not snapshot JJ
// or refresh Git's index: preserving these bytes is stronger than comparing logs.
type fileState struct {
	Hash  [32]byte
	Mode  os.FileMode
	Mtime int64
	Link  string
}

func inventoryOptional(root string) (map[string]fileState, error) {
	if _, err := os.Lstat(root); os.IsNotExist(err) {
		return map[string]fileState{}, nil
	}
	return inventory(root)
}
func inventory(root string) (map[string]fileState, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("not a directory: %s", root)
	}
	device := info.Sys().(*syscall.Stat_t).Dev
	states := map[string]fileState{}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Sys().(*syscall.Stat_t).Dev != device {
			return fmt.Errorf("nested mount unsupported: %s", path)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if d.IsDir() {
			states[rel] = fileState{Mode: info.Mode()}
			return nil
		}
		state := fileState{Mode: info.Mode(), Mtime: info.ModTime().UnixNano()}
		if d.Type()&os.ModeSymlink != 0 {
			if within(path, filepath.Join(root, ".git")) || within(path, filepath.Join(root, ".jj")) {
				return fmt.Errorf("symlinked VC metadata unsupported: %s", path)
			}
			state.Link, err = os.Readlink(path)
		} else if info.Mode().IsRegular() {
			var data []byte
			data, err = os.ReadFile(path)
			state.Hash = sha256.Sum256(data)
		} else {
			return fmt.Errorf("special file unsupported: %s", path)
		}
		if err != nil {
			return err
		}
		states[rel] = state
		return nil
	})
	return states, err
}

func command(root, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = root
	// Do not allow caller-specific repository routing or JJ configuration overrides
	// to make validation inspect a different checkout.
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if strings.HasPrefix(key, "GIT_") || strings.HasPrefix(key, "JJ_") {
			continue
		}
		cmd.Env = append(cmd.Env, item)
	}
	cmd.Env = append(cmd.Env, "GIT_OPTIONAL_LOCKS=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s %v: %w: %s", name, args, err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func checkCheckout(root string) (string, error) {
	gitDir := filepath.Join(root, ".git")
	info, err := os.Lstat(gitDir)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("requires independent .git directory: %s", root)
	}
	for _, rel := range []string{"commondir", "gitdir", "worktrees", "modules", "objects/info/alternates", "objects/info/http-alternates"} {
		if _, err := os.Lstat(filepath.Join(gitDir, rel)); !os.IsNotExist(err) {
			return "", fmt.Errorf("unsupported Git layout: .git/%s", rel)
		}
	}
	if err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root || (d.Name() != ".git" && d.Name() != ".jj") {
			return nil
		}
		if filepath.Dir(path) == root {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		return fmt.Errorf("nested VC checkout unsupported: %s", path)
	}); err != nil {
		return "", err
	}
	top, err := command(root, "git", "-c", "core.fsmonitor=false", "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	if top != root {
		return "", fmt.Errorf("Git root mismatch: %s", top)
	}
	config, err := command(root, "git", "config", "--local", "--list")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(config, "\n") {
		key, value, _ := strings.Cut(line, "=")
		if key == "core.worktree" || strings.HasPrefix(key, "include") || key == "extensions.worktreeconfig" || (key == "core.bare" && value != "false") {
			return "", fmt.Errorf("unsupported location-dependent Git config: %s", key)
		}
	}
	effective, err := command(root, "git", "config", "--list")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(effective, "\n") {
		key, _, _ := strings.Cut(line, "=")
		if key == "core.worktree" {
			return "", fmt.Errorf("unsupported effective Git core.worktree configuration")
		}
	}
	arrangement := "independent Git clone"
	jjDir := filepath.Join(root, ".jj")
	info, err = os.Lstat(jjDir)
	if os.IsNotExist(err) {
		return arrangement, nil
	}
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("linked JJ metadata unsupported")
	}
	info, err = os.Lstat(filepath.Join(jjDir, "repo"))
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("requires independent .jj/repo directory")
	}
	target, err := os.ReadFile(filepath.Join(jjDir, "repo", "store", "git_target"))
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(string(target)) != "../../../.git" {
		return "", fmt.Errorf("unsupported JJ Git target: %q", target)
	}
	version, err := command(root, "jj", "--version")
	if err != nil {
		return "", err
	}
	if version != "jj 0.43.0" {
		return "", fmt.Errorf("unproven JJ version %q (supported: 0.43.0)", version)
	}
	workspaces, err := command(root, "jj", "--ignore-working-copy", "--no-pager", "workspace", "list")
	if err != nil {
		return "", err
	}
	if len(strings.Split(workspaces, "\n")) != 1 || workspaces == "" {
		return "", fmt.Errorf("additional/unknown JJ workspaces unsupported")
	}
	jjRoot, err := command(root, "jj", "--ignore-working-copy", "--no-pager", "root")
	if err != nil {
		return "", err
	}
	if jjRoot != root {
		return "", fmt.Errorf("JJ root mismatch: %s", jjRoot)
	}
	return arrangement + " with colocated JJ 0.43.0 (relative Git target)", nil
}

func unchanged(root string, expected map[string]fileState) error {
	actual, err := inventory(root)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(expected, actual) {
		return fmt.Errorf("filesystem inventory mismatch: %s", root)
	}
	return nil
}
