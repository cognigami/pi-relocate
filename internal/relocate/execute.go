package relocate

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"
)

func (p *Plan) boundary(name string) error {
	if p.fail != nil {
		return p.fail(name)
	}
	return nil
}

// Execute retains backups and reports partial progress rather than pretending
// several filesystem renames are an atomic transaction. Callers must stop users
// of the checkout and sessions; concurrent modification is not supported.
func (p *Plan) Execute(w io.Writer) (result error) {
	checkoutAt, sessionsAt, recovery := p.Source, p.SourceBucket, "not created"
	completed := []string{}
	defer func() {
		if result != nil {
			result = fmt.Errorf("%w\nCompleted: %s\nCheckout location: %s\nSession collection location: %s\nRecovery copies: %s\nInspect retained staging files and RECOVERY.md, restore metadata originals and rename checkout/session collection back as needed. Do not blindly rerun a partial relocation.", result, strings.Join(completed, "; "), checkoutAt, sessionsAt, recovery)
		}
	}()
	// Revalidate the bytes on which the in-memory plan depends before any writes.
	if err := unchanged(p.Source, p.checkout); err != nil {
		return err
	}
	current, err := inventoryOptional(p.SourceBucket)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(current, p.bucket) {
		return fmt.Errorf("source session bucket changed since discovery")
	}
	for _, f := range p.files {
		if err := originalStillPresent(f.Source, f); err != nil {
			return err
		}
	}
	for _, path := range []string{p.Destination, p.DestinationBucket} {
		if err := absent(path); err != nil {
			return err
		}
	}
	if err := p.boundary("backup"); err != nil {
		return err
	}
	recoveryParent := filepath.Dir(p.options.Storage)
	if err := os.MkdirAll(recoveryParent, 0700); err != nil {
		return err
	}
	recovery, err = os.MkdirTemp(recoveryParent, "relo-recovery-")
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "Recovery copies: %s\n", recovery)
	token := make([]byte, 12)
	if _, err := rand.Read(token); err != nil {
		return err
	}
	suffix := fmt.Sprintf(".relo-%x", token)
	stage := filepath.Join(filepath.Dir(p.Source), suffix)
	if err := absent(stage); err != nil {
		return err
	}
	fmt.Fprintf(w, "Checkout staging (if required): %s\nSession replacement staging: <target>%s\n", stage, suffix)
	var recoveryText strings.Builder
	fmt.Fprintf(&recoveryText, "# Recovery\n\nCheckout: %q -> %q\nCheckout staging: %q\nSession bucket: %q -> %q\nReplacement staging suffix: %q\n\nStop affected processes. Inspect actual locations; this file is a pre-move recovery map, not a completion journal. Never overwrite the only surviving checkout or session copy. To undo a partial move, restore each original below to its source path (moving the bucket back first if appropriate), then rename the checkout back. For a container move, stage the checkout as a sibling, remove only the empty container, then rename back. Retain these copies until operator acceptance.\n\n", p.Source, p.Destination, stage, p.SourceBucket, p.DestinationBucket, suffix)
	for i, f := range p.files {
		fmt.Fprintf(&recoveryText, "- %04d.jsonl: original %q; planned target %q\n", i, f.Source, f.Target)
	}
	if err := writeNew(filepath.Join(recovery, "RECOVERY.md"), []byte(recoveryText.String()), 0600, time.Now()); err != nil {
		return err
	}
	for i, f := range p.files {
		if err := p.boundary("backup-write"); err != nil {
			return err
		}
		if err := writeNew(filepath.Join(recovery, fmt.Sprintf("%04d.jsonl", i)), f.Before, f.Mode, f.Mtime); err != nil {
			return err
		}
	}
	// Verify every backup, including originals of external inbound references.
	for i, f := range p.files {
		backup := filepath.Join(recovery, fmt.Sprintf("%04d.jsonl", i))
		data, err := os.ReadFile(backup)
		if err != nil {
			return err
		}
		if !bytes.Equal(data, f.Before) {
			return fmt.Errorf("backup verification failed: %s", backup)
		}
	}
	completed = append(completed, "session originals backed up")
	fmt.Fprintln(w, "Moving checkout...")
	if err := p.boundary("checkout-staging"); err != nil {
		return err
	}
	if filepath.Dir(p.Destination) == p.Source {
		if err := absent(stage); err != nil {
			return err
		}
		if err := os.Rename(p.Source, stage); err != nil {
			return err
		}
		checkoutAt = stage
		completed = append(completed, "checkout staged")
		if err := p.boundary("checkout-staged"); err != nil {
			return err
		}
		if err := os.Mkdir(p.Source, 0755); err != nil {
			return err
		}
		if err := absent(p.Destination); err != nil {
			return err
		}
		if err := os.Rename(stage, p.Destination); err != nil {
			return err
		}
	} else {
		if err := os.MkdirAll(filepath.Dir(p.Destination), 0755); err != nil {
			return err
		}
		if err := absent(p.Destination); err != nil {
			return err
		}
		if err := os.Rename(p.Source, p.Destination); err != nil {
			return err
		}
	}
	checkoutAt = p.Destination
	completed = append(completed, "checkout relocated")
	fmt.Fprintln(w, "Relocating session collection and unchanged sidecars...")
	if _, err := os.Lstat(p.SourceBucket); err == nil {
		if err := absent(p.DestinationBucket); err != nil {
			return err
		}
		if err := os.Rename(p.SourceBucket, p.DestinationBucket); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	sessionsAt = p.DestinationBucket
	completed = append(completed, "session collection relocated")
	for _, f := range p.files {
		if f.Source == f.Target {
			continue
		}
		if err := p.boundary("session-publication"); err != nil {
			return err
		}
		if err := publish(f, suffix); err != nil {
			return err
		}
		completed = append(completed, "published "+f.Target)
	}
	fmt.Fprintln(w, "Updating recognized external inbound references...")
	for _, f := range p.files {
		if f.Source != f.Target {
			continue
		}
		if err := p.boundary("external-reference"); err != nil {
			return err
		}
		if err := publish(f, suffix); err != nil {
			return err
		}
		completed = append(completed, "published external "+f.Target)
	}
	fmt.Fprintln(w, "Verifying durable checkout, sessions, sidecars and references...")
	if err := p.boundary("verification"); err != nil {
		return err
	}
	if err := p.verify(); err != nil {
		return err
	}
	fmt.Fprintf(w, "Checkout and session files relocated; durable metadata verified.\nCheckout: %s\nSessions: %s\nRetained recovery copies: %s\n", p.Destination, p.DestinationBucket, recovery)
	return nil
}

func absent(path string) error {
	_, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("destination/staging collision: %s", path)
}
func originalStillPresent(path string, f sessionFile) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode() != f.Mode || !info.ModTime().Equal(f.Mtime) {
		return fmt.Errorf("session changed since discovery: %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(data, f.Before) {
		return fmt.Errorf("session bytes changed since discovery: %s", path)
	}
	return nil
}
func writeNew(path string, data []byte, mode os.FileMode, mtime time.Time) (result error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() {
		if err := file.Close(); result == nil {
			result = err
		}
	}()
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Chmod(mode); err != nil {
		return err
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		return err
	}
	return file.Sync()
}
func publish(f sessionFile, suffix string) error {
	if err := originalStillPresent(f.Target, f); err != nil {
		return err
	}
	stage := f.Target + suffix
	if err := writeNew(stage, f.After, f.Mode, f.Mtime); err != nil {
		return fmt.Errorf("prepare replacement %s: %w", stage, err)
	}
	if err := os.Rename(stage, f.Target); err != nil {
		return fmt.Errorf("publish replacement %s: %w", stage, err)
	}
	return nil
}
func (p *Plan) verify() error {
	if _, err := checkCheckout(p.Destination); err != nil {
		return err
	}
	if err := unchanged(p.Destination, p.checkout); err != nil {
		return err
	}
	expected := make(map[string]fileState, len(p.bucket))
	for k, v := range p.bucket {
		expected[k] = v
	}
	for _, f := range p.files {
		data, err := os.ReadFile(f.Target)
		if err != nil {
			return err
		}
		info, err := os.Stat(f.Target)
		if err != nil {
			return err
		}
		if !bytes.Equal(data, f.After) || info.Mode() != f.Mode || !info.ModTime().Equal(f.Mtime) {
			return fmt.Errorf("session verification failed: %s", f.Target)
		}
		if f.Source != f.Target {
			h, err := header(data)
			if err != nil {
				return err
			}
			if h.Cwd != p.Destination {
				return fmt.Errorf("session ownership mismatch: %s", f.Target)
			}
			rel, err := filepath.Rel(p.DestinationBucket, f.Target)
			if err != nil {
				return err
			}
			state := expected[rel]
			state.Hash = sha256.Sum256(data)
			expected[rel] = state
		}
	}
	actual, err := inventoryOptional(p.DestinationBucket)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(expected, actual) {
		return fmt.Errorf("session/sidecar inventory mismatch at %s", p.DestinationBucket)
	}
	if err := absent(p.SourceBucket); err != nil {
		return err
	}
	if filepath.Dir(p.Destination) != p.Source {
		if err := absent(p.Source); err != nil {
			return err
		}
	}
	return nil
}
