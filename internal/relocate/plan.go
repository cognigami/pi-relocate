// Package relocate moves an independent checkout and its default-storage Pi sessions.
package relocate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type Options struct {
	Storage string
	Caller  string
	Config  string
	// Tests supply isolated storage and a caller outside the fixture checkout.
}

type sessionFile struct {
	Source, Target string
	Before, After  []byte
	Mode           os.FileMode
	Mtime          time.Time
	Updates        int
}

type Plan struct {
	Source, Destination             string
	SourceBucket, DestinationBucket string
	Arrangement                     string
	Sessions, Sidecars, Orphans     int
	Warnings                        []string
	files                           []sessionFile
	checkout                        map[string]fileState
	bucket                          map[string]fileState
	options                         Options
	// A narrow boundary hook for deterministic failure tests, never CLI configuration.
	fail func(string) error
}

func DefaultOptions() (Options, error) {
	for _, name := range []string{"PI_CODING_AGENT_SESSION_DIR", "PI_CODING_AGENT_DIR"} {
		if os.Getenv(name) != "" {
			return Options{}, fmt.Errorf("unsupported storage customization: %s", name)
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Options{}, err
	}
	caller, err := os.Getwd()
	if err != nil {
		return Options{}, err
	}
	return Options{Storage: filepath.Join(home, ".pi", "agent", "sessions"), Caller: caller, Config: filepath.Join(home, ".config", "pi-extensions", "sandbox", "99-mini.jsonc")}, nil
}

// BucketName follows Pi's getDefaultSessionDir path encoding, not URL encoding.
func BucketName(path string) string {
	return "--" + strings.NewReplacer("/", "-", "\\", "-", ":", "-").Replace(strings.TrimLeft(path, "/\\")) + "--"
}

func within(path, parent string) bool {
	rel, err := filepath.Rel(parent, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// rejectAliases checks every existing component; nonexistent destination suffixes are allowed.
func rejectAliases(path string) error {
	for p := path; ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink/aliased path is unsupported: %s", p)
		}
		if filepath.Dir(p) == p {
			break
		}
		if err == nil {
			entries, e := os.ReadDir(filepath.Dir(p))
			if e != nil {
				return e
			}
			exact := false
			for _, entry := range entries {
				if entry.Name() == filepath.Base(p) {
					exact = true
					break
				}
			}
			if !exact {
				return fmt.Errorf("noncanonical path spelling: %s", p)
			}
		}
	}
	existing := path
	for {
		_, err := os.Stat(existing)
		if err == nil {
			break
		}
		if !os.IsNotExist(err) {
			return err
		}
		existing = filepath.Dir(existing)
	}
	resolved, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return err
	}
	// Reject rather than silently changing the session ownership key.
	if filepath.Clean(resolved) != existing {
		return fmt.Errorf("noncanonical path: %s (use %s)", existing, resolved)
	}
	return nil
}

// Inspect lexical input components before filepath.Abs cleans away a possible
// symlink/.. pair. The canonical path check below then checks on-disk spelling.
func rejectInputAliases(path string) error {
	prefix := string(filepath.Separator)
	if !filepath.IsAbs(path) {
		var err error
		prefix, err = os.Getwd()
		if err != nil {
			return err
		}
	}
	for _, part := range strings.Split(path, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		prefix = filepath.Join(prefix, part)
		info, err := os.Lstat(prefix)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink/aliased input is unsupported: %s", prefix)
		}
	}
	return nil
}

func nearest(path string) (os.FileInfo, error) {
	for {
		i, err := os.Stat(path)
		if err == nil {
			return i, nil
		}
		if !os.IsNotExist(err) {
			return nil, err
		}
		next := filepath.Dir(path)
		if next == path {
			return nil, err
		}
		path = next
	}
}
func sameDevice(a, b string) error {
	x, err := nearest(a)
	if err != nil {
		return err
	}
	y, err := nearest(b)
	if err != nil {
		return err
	}
	if x.Sys().(*syscall.Stat_t).Dev != y.Sys().(*syscall.Stat_t).Dev {
		return fmt.Errorf("cross-device move unsupported: %s -> %s", a, b)
	}
	return nil
}

func Discover(source, destination string, options Options) (*Plan, error) {
	if !filepath.IsAbs(options.Caller) {
		return nil, fmt.Errorf("caller must be absolute")
	}
	if !filepath.IsAbs(source) {
		source = options.Caller + string(filepath.Separator) + source
	}
	if !filepath.IsAbs(destination) {
		destination = options.Caller + string(filepath.Separator) + destination
	}
	for _, path := range []string{source, destination} {
		if err := rejectInputAliases(path); err != nil {
			return nil, err
		}
	}
	source, err := filepath.Abs(source)
	if err != nil {
		return nil, err
	}
	destination, err = filepath.Abs(destination)
	if err != nil {
		return nil, err
	}
	for _, p := range []string{source, destination, options.Storage, options.Caller} {
		if !filepath.IsAbs(p) {
			return nil, fmt.Errorf("internal location must be absolute: %s", p)
		}
		if err := rejectAliases(p); err != nil {
			return nil, err
		}
	}
	if within(options.Caller, source) {
		return nil, fmt.Errorf("caller must be outside source checkout")
	}
	if within(source, destination) {
		return nil, fmt.Errorf("destination is source or its ancestor")
	}
	if within(destination, source) && filepath.Dir(destination) != source {
		return nil, fmt.Errorf("only a direct child destination is supported inside source")
	}
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		return nil, fmt.Errorf("destination already exists or cannot be inspected: %s", destination)
	}
	if err := sameDevice(source, filepath.Dir(destination)); err != nil {
		return nil, err
	}
	if filepath.Dir(destination) == source {
		if err := sameDevice(source, filepath.Dir(source)); err != nil {
			return nil, err
		}
	}
	if within(options.Storage, source) || within(options.Storage, destination) {
		return nil, fmt.Errorf("Pi storage must be outside checkout")
	}
	for _, settings := range []string{filepath.Join(filepath.Dir(options.Storage), "settings.json"), filepath.Join(source, ".pi", "settings.json"), filepath.Join(options.Caller, ".pi", "settings.json")} {
		if err := rejectCustomStorage(settings); err != nil {
			return nil, err
		}
	}
	arrangement, err := checkCheckout(source)
	if err != nil {
		return nil, err
	}
	p := &Plan{Source: source, Destination: destination, Arrangement: arrangement, options: options}
	p.SourceBucket = filepath.Join(options.Storage, BucketName(source))
	p.DestinationBucket = filepath.Join(options.Storage, BucketName(destination))
	if p.SourceBucket == p.DestinationBucket {
		return nil, fmt.Errorf("source/destination session encoding collision")
	}
	if _, err := os.Lstat(p.DestinationBucket); !os.IsNotExist(err) {
		return nil, fmt.Errorf("destination session bucket exists or cannot be inspected: %s", p.DestinationBucket)
	}
	if err := sameDevice(p.SourceBucket, options.Storage); err != nil {
		return nil, err
	}
	p.checkout, err = inventory(source)
	if err != nil {
		return nil, err
	}
	p.bucket, err = inventoryOptional(p.SourceBucket)
	if err != nil {
		return nil, err
	}
	mapping := map[string]string{}
	ids := map[string]bool{}
	entries, err := os.ReadDir(p.SourceBucket)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			return nil, fmt.Errorf("unsupported bucket entry: %s", entry.Name())
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".sidecar.json") {
			p.Sidecars++
			continue
		}
		if !strings.HasSuffix(name, ".jsonl") {
			return nil, fmt.Errorf("unknown session bucket file: %s", name)
		}
		path := filepath.Join(p.SourceBucket, name)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		h, err := header(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if h.Cwd != source {
			return nil, fmt.Errorf("mixed-owner or aliased bucket: %s owns %q, expected %q", path, h.Cwd, source)
		}
		if ids[h.ID] {
			return nil, fmt.Errorf("duplicate session ID: %s", h.ID)
		}
		ids[h.ID] = true
		mapping[path] = filepath.Join(p.DestinationBucket, name)
		p.Sessions++
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".sidecar.json") {
			session := strings.TrimSuffix(entry.Name(), ".sidecar.json") + ".jsonl"
			if _, ok := mapping[filepath.Join(p.SourceBucket, session)]; !ok {
				p.Orphans++
			}
		}
	}
	err = filepath.WalkDir(options.Storage, func(path string, d os.DirEntry, walkErr error) error {
		if os.IsNotExist(walkErr) && path == options.Storage {
			return nil
		}
		if walkErr != nil {
			return walkErr
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink in default session storage: %s", path)
		}
		if d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		target, moved := mapping[path]
		if !moved {
			target = path
		}
		// Duplicate IDs outside the moving bucket cannot be disambiguated safely.
		if !moved {
			if h, e := header(data); e == nil {
				if ids[h.ID] {
					return fmt.Errorf("session ID collision in %s", path)
				}
				if h.Cwd == source {
					return fmt.Errorf("source-owned session outside its default bucket: %s", path)
				}
			}
		}
		if err := unsupportedReference(data, source, mapping); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		after, n, err := Transform(data, source, destination, mapping, moved)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if moved || n > 0 {
			if _, err := header(data); err != nil {
				return fmt.Errorf("affected session %s: %w", path, err)
			}
			info, err := os.Stat(path)
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("not a regular session: %s", path)
			}
			if info.Sys().(*syscall.Stat_t).Nlink != 1 {
				return fmt.Errorf("hard-linked session unsupported: %s", path)
			}
			p.files = append(p.files, sessionFile{path, target, data, after, info.Mode(), info.ModTime(), n})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if options.Config != "" {
		data, err := os.ReadFile(options.Config)
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		if bytes.Contains(data, []byte(source)) {
			p.Warnings = append(p.Warnings, fmt.Sprintf("Review %s separately: mini project selector and local staging path must follow the new checkout, not family siblings; its remote working directory must NOT automatically change.", options.Config))
		}
	}
	p.Warnings = append(p.Warnings, "Other project-local absolute paths and unsupported extension operational schemas are not migrated. Stop affected processes before execution. Index rebuild and interactive acceptance remain manual.")
	return p, nil
}

func rejectCustomStorage(path string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf}), &settings); err != nil {
		return fmt.Errorf("cannot establish session storage from %s: %w", path, err)
	}
	if raw, ok := settings["sessionDir"]; ok && string(raw) != "null" && string(raw) != `""` {
		return fmt.Errorf("unsupported sessionDir customization in %s", path)
	}
	return nil
}

type sessionHeader struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Cwd  string `json:"cwd"`
}

func header(data []byte) (sessionHeader, error) {
	line, _, _ := bytes.Cut(data, []byte{'\n'})
	var h sessionHeader
	if err := json.Unmarshal(line, &h); err != nil {
		return h, fmt.Errorf("invalid session header: %w", err)
	}
	if h.Type != "session" || h.ID == "" || h.Cwd == "" {
		return h, fmt.Errorf("missing session type/id/cwd")
	}
	return h, nil
}

func (p *Plan) Preview(w io.Writer) {
	fmt.Fprintf(w, "Checkout: %s -> %s\nArrangement: %s\nSessions: %s -> %s\n%d sessions; %d sidecars (%d orphans)\n", p.Source, p.Destination, p.Arrangement, p.SourceBucket, p.DestinationBucket, p.Sessions, p.Sidecars, p.Orphans)
	for _, f := range p.files {
		if f.Updates > 0 {
			if f.Source == f.Target {
				fmt.Fprintf(w, "External reference update: %s (%d fields)\n", f.Source, f.Updates)
			} else {
				fmt.Fprintf(w, "Session metadata: %s (%d fields)\n", filepath.Base(f.Source), f.Updates)
			}
		}
	}
	for _, warning := range p.Warnings {
		fmt.Fprintf(w, "Warning: %s\n", warning)
	}
}
