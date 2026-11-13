package relocate

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const recoveryPrefix = "relo-recovery-"

// CleanupPlan selects one retained recovery directory, never a live session bucket.
// The recovery map describes intent, not successful completion: the operator must
// accept the relocation before authorizing deletion.
type CleanupPlan struct {
	Directory, Source, Destination string
	files                          map[string]fileState
	directoryInfo                  os.FileInfo
}

func recoveryName(name string) bool {
	suffix, ok := strings.CutPrefix(name, recoveryPrefix)
	if !ok || suffix == "" {
		return false
	}
	for _, c := range suffix {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func recoveryCheckout(directory string) (string, string, error) {
	path := filepath.Join(directory, "RECOVERY.md")
	info, err := os.Lstat(path)
	if err != nil {
		return "", "", err
	}
	if !info.Mode().IsRegular() {
		return "", "", fmt.Errorf("recovery map must be a regular file: %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) < 3 || lines[0] != "# Recovery" || lines[1] != "" {
		return "", "", fmt.Errorf("unrecognized recovery map: %s", path)
	}
	var source, destination string
	if n, err := fmt.Sscanf(lines[2], "Checkout: %q -> %q", &source, &destination); err != nil || n != 2 || lines[2] != fmt.Sprintf("Checkout: %q -> %q", source, destination) || !filepath.IsAbs(source) || !filepath.IsAbs(destination) {
		return "", "", fmt.Errorf("invalid checkout paths in recovery map: %s", path)
	}
	return source, destination, nil
}

// DiscoverCleanup resolves an original checkout basename, or an exact recovery
// directory name/path. Multiple matching relocations require an explicit directory.
func DiscoverCleanup(selector string, options Options) (*CleanupPlan, error) {
	if selector == "" || !filepath.IsAbs(options.Storage) || !filepath.IsAbs(options.Caller) {
		return nil, fmt.Errorf("cleanup requires a selector and absolute storage/caller paths")
	}
	root := filepath.Dir(options.Storage)
	if err := rejectAliases(root); err != nil {
		return nil, err
	}
	for _, settings := range []string{filepath.Join(root, "settings.json"), filepath.Join(options.Caller, ".pi", "settings.json")} {
		if err := rejectCustomStorage(settings); err != nil {
			return nil, err
		}
	}
	var selected string
	if recoveryName(selector) {
		selected = filepath.Join(root, selector)
	} else if strings.ContainsRune(selector, filepath.Separator) {
		selected = selector
		if !filepath.IsAbs(selected) {
			selected = filepath.Join(options.Caller, selected)
		}
		if err := rejectInputAliases(selected); err != nil {
			return nil, err
		}
		selected = filepath.Clean(selected)
	} else {
		entries, err := os.ReadDir(root)
		if err != nil {
			return nil, err
		}
		var matches []string
		for _, entry := range entries {
			if !recoveryName(entry.Name()) || !entry.IsDir() {
				continue
			}
			directory := filepath.Join(root, entry.Name())
			source, _, err := recoveryCheckout(directory)
			if err != nil {
				return nil, fmt.Errorf("cannot identify %s: %w; select an exact recovery directory instead", directory, err)
			}
			if filepath.Base(source) == selector {
				matches = append(matches, directory)
			}
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("no recovery directory for original checkout name %q", selector)
		}
		if len(matches) != 1 {
			return nil, fmt.Errorf("ambiguous checkout name %q; select one exact recovery directory:\n%s", selector, strings.Join(matches, "\n"))
		}
		selected = matches[0]
	}
	if filepath.Dir(selected) != root || !recoveryName(filepath.Base(selected)) {
		return nil, fmt.Errorf("cleanup is restricted to %s<digits> directories directly under %s", recoveryPrefix, root)
	}
	if err := rejectAliases(selected); err != nil {
		return nil, err
	}
	directoryInfo, err := os.Lstat(selected)
	if err != nil {
		return nil, err
	}
	if !directoryInfo.IsDir() {
		return nil, fmt.Errorf("recovery path must be a directory: %s", selected)
	}
	source, destination, err := recoveryCheckout(selected)
	if err != nil {
		return nil, err
	}
	files, err := inventory(selected)
	if err != nil {
		return nil, err
	}
	for name, state := range files {
		if name == "." {
			continue
		}
		if !state.Mode.IsRegular() || (name != "RECOVERY.md" && !backupName(name)) {
			return nil, fmt.Errorf("unexpected recovery entry; will not delete: %s", filepath.Join(selected, name))
		}
	}
	return &CleanupPlan{Directory: selected, Source: source, Destination: destination, files: files, directoryInfo: directoryInfo}, nil
}

func backupName(name string) bool {
	digits, ok := strings.CutSuffix(name, ".jsonl")
	if !ok || len(digits) < 4 {
		return false
	}
	for _, c := range digits {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func (p *CleanupPlan) Preview(w io.Writer) {
	fmt.Fprintf(w, "Recovery cleanup: %s\nCheckout: %s -> %s\nDelete %d session originals and RECOVERY.md.\n", p.Directory, p.Source, p.Destination, len(p.files)-2)
	fmt.Fprintln(w, "Warning: permanently removes rollback copies. Only proceed after accepting the relocation; RECOVERY.md is not proof of success.")
}

func (p *CleanupPlan) Execute(w io.Writer) error {
	if err := rejectAliases(p.Directory); err != nil {
		return err
	}
	parent, err := os.OpenRoot(filepath.Dir(p.Directory))
	if err != nil {
		return err
	}
	defer parent.Close()
	base := filepath.Base(p.Directory)
	directory, err := parent.OpenRoot(base)
	if err != nil {
		return err
	}
	defer directory.Close()
	info, err := directory.Stat(".")
	if err != nil {
		return err
	}
	if !os.SameFile(info, p.directoryInfo) {
		return fmt.Errorf("recovery directory changed since discovery: %s", p.Directory)
	}
	if err := unchanged(p.Directory, p.files); err != nil {
		return err
	}
	var names []string
	for name := range p.files {
		if name != "." {
			names = append(names, name)
		}
	}
	// Keep the restoration map until every original has been removed. No
	// recursive deletion: an unexpected entry prevents removing the directory.
	sort.Strings(names)
	fmt.Fprintf(w, "Deleting recovery copies: %s\n", p.Directory)
	current, err := parent.Lstat(base)
	if err != nil || !os.SameFile(info, current) {
		return fmt.Errorf("recovery directory path changed before deletion: %s", p.Directory)
	}
	for i, name := range names {
		if err := directory.Remove(name); err != nil {
			return fmt.Errorf("removed %d of %d recovery files; inspect %s: %w", i, len(names), p.Directory, err)
		}
	}
	current, err = parent.Lstat(base)
	if err != nil || !os.SameFile(info, current) {
		return fmt.Errorf("recovery files removed but directory path changed: %s", p.Directory)
	}
	if err := parent.Remove(base); err != nil {
		return fmt.Errorf("recovery files removed but directory could not be removed: %s: %w", p.Directory, err)
	}
	fmt.Fprintf(w, "Deleted recovery directory: %s\nLive checkouts and sessions were not changed.\n", p.Directory)
	return nil
}
