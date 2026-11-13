package relocate

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCleanupLegacyMapAndEmptyBackup(t *testing.T) {
	root := fixtureRoot(t)
	options := Options{Storage: filepath.Join(root, "home", ".pi", "agent", "sessions"), Caller: root}
	directory := filepath.Join(filepath.Dir(options.Storage), "relo-recovery-123")
	source := filepath.Join(root, "quoted \"project\" with spaces")
	// This is the existing map format, with no new metadata or completion marker.
	put(t, filepath.Join(directory, "RECOVERY.md"), []byte(fmt.Sprintf("# Recovery\n\nCheckout: %q -> %q\nCheckout staging: %q\n", source, source+"-moved", filepath.Join(root, ".relo-stage"))))
	plan, err := DiscoverCleanup(filepath.Base(source), options)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Source != source || plan.Destination != source+"-moved" || plan.Directory != directory {
		t.Fatalf("unexpected selection: %+v", plan)
	}
	var out bytes.Buffer
	plan.Preview(&out)
	if !strings.Contains(out.String(), "0 session originals and RECOVERY.md") {
		t.Fatal(out.String())
	}
	if err := plan.Execute(&out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(directory); !os.IsNotExist(err) {
		t.Fatalf("cleanup did not remove empty-original recovery: %v", err)
	}
}

func TestCleanupExactSelectionWithUnidentifiableOtherMap(t *testing.T) {
	root := fixtureRoot(t)
	options := Options{Storage: filepath.Join(root, "home", ".pi", "agent", "sessions"), Caller: root}
	parent := filepath.Dir(options.Storage)
	put(t, filepath.Join(parent, "relo-recovery-111", "RECOVERY.md"), []byte(fmt.Sprintf("# Recovery\n\nCheckout: %q -> %q\n", filepath.Join(root, "perry"), filepath.Join(root, "perry", "root"))))
	other := filepath.Join(parent, "relo-recovery-222", "RECOVERY.md")
	put(t, other, []byte("interrupted backup before map completed\n"))
	if _, err := DiscoverCleanup("perry", options); err == nil || !strings.Contains(err.Error(), "cannot identify") {
		t.Fatalf("ambiguous inventory should not guess ownership: %v", err)
	}
	plan, err := DiscoverCleanup("relo-recovery-111", options)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Execute(&bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(other); err != nil || string(data) != "interrupted backup before map completed\n" {
		t.Fatalf("cleanup touched unrelated incomplete backup: %q %v", data, err)
	}
}
