package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func commandRecovery(t *testing.T, f *commandFixture, name, source string) string {
	t.Helper()
	directory := filepath.Join(f.agent, name)
	commandWrite(t, filepath.Join(directory, "RECOVERY.md"), []byte(fmt.Sprintf("# Recovery\n\nCheckout: %q -> %q\n", source, source+"-moved")), 0600)
	commandWrite(t, filepath.Join(directory, "0000.jsonl"), []byte("synthetic original zero\n"), 0600)
	commandWrite(t, filepath.Join(directory, "0001.jsonl"), []byte("synthetic original one\n"), 0600)
	return directory
}

func TestRunCleanupAfterRelocation(t *testing.T) {
	f := newCommandFixture(t)
	source := f.checkout(t, "perry")
	destination := filepath.Join(source, "root")
	collection := f.collection(t, source)
	var combined bytes.Buffer
	if code := run([]string{source, destination}, &combined, &combined); code != 0 {
		t.Fatalf("relocation: code=%d\n%s", code, &combined)
	}
	f.assertRecovery(t, collection.originals)
	entries, err := os.ReadDir(f.agent)
	if err != nil {
		t.Fatal(err)
	}
	var recovery string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "relo-recovery-") {
			if recovery != "" {
				t.Fatal("fixture unexpectedly has multiple recoveries")
			}
			recovery = filepath.Join(f.agent, entry.Name())
		}
	}
	other := commandRecovery(t, f, "relo-recovery-123", filepath.Join(f.root, "engine1"))
	before := commandSnapshot(t, f.root)
	combined.Reset()
	if code := run([]string{"cleanup", "--dry-run", "perry"}, &combined, &combined); code != 0 {
		t.Fatalf("cleanup preview: code=%d\n%s", code, &combined)
	}
	for _, expected := range []string{recovery, "3 session originals and RECOVERY.md", "not proof of success", "Preview validated. No changes made."} {
		if !strings.Contains(combined.String(), expected) {
			t.Errorf("cleanup preview lacks %q:\n%s", expected, &combined)
		}
	}
	assertNoCommandReminder(t, combined.String())
	assertCommandSnapshot(t, f.root, before)
	checkoutBefore := commandSnapshot(t, destination)
	sessionsBefore := commandSnapshot(t, f.storage)
	otherBefore := commandSnapshot(t, other)
	homeBefore := f.protectedHome(t)
	combined.Reset()
	if code := run([]string{"cleanup", "perry"}, &combined, &combined); code != 0 {
		t.Fatalf("cleanup: code=%d\n%s", code, &combined)
	}
	assertCommandAbsent(t, recovery)
	assertCommandSnapshot(t, destination, checkoutBefore)
	assertCommandSnapshot(t, f.storage, sessionsBefore)
	assertCommandSnapshot(t, other, otherBefore)
	f.assertProtectedHome(t, homeBefore)
	if !strings.Contains(combined.String(), "Deleted recovery directory: "+recovery) || strings.Contains(combined.String(), "No changes made") {
		t.Fatalf("incorrect cleanup success output:\n%s", &combined)
	}
	assertNoCommandReminder(t, combined.String())
}

func TestRunCleanupAmbiguityAndExplicitSelection(t *testing.T) {
	for _, kind := range []string{"name", "absolute", "relative"} {
		t.Run(kind, func(t *testing.T) {
			f := newCommandFixture(t)
			first := commandRecovery(t, f, "relo-recovery-111", filepath.Join(f.root, "a", "perry"))
			second := commandRecovery(t, f, "relo-recovery-222", filepath.Join(f.root, "b", "perry"))
			before := commandSnapshot(t, f.root)
			var combined bytes.Buffer
			if code := run([]string{"cleanup", "perry"}, &combined, &combined); code != 1 || !strings.Contains(combined.String(), "ambiguous") || !strings.Contains(combined.String(), first) || !strings.Contains(combined.String(), second) {
				t.Fatalf("ambiguous cleanup: code=%d\n%s", code, &combined)
			}
			assertCommandSnapshot(t, f.root, before)
			selector := filepath.Base(first)
			if kind == "absolute" {
				selector = first
			} else if kind == "relative" {
				var err error
				selector, err = filepath.Rel(f.root, first)
				if err != nil {
					t.Fatal(err)
				}
			}
			secondBefore := commandSnapshot(t, second)
			combined.Reset()
			if code := run([]string{"cleanup", selector}, &combined, &combined); code != 0 {
				t.Fatalf("explicit cleanup: code=%d\n%s", code, &combined)
			}
			assertCommandAbsent(t, first)
			assertCommandSnapshot(t, second, secondBefore)
		})
	}
}

func TestRunCleanupRefusalDoesNotDelete(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*testing.T, *commandFixture, string) string
	}{
		{"unknown project", func(t *testing.T, f *commandFixture, dir string) string { return "unknown" }},
		{"malformed map", func(t *testing.T, f *commandFixture, dir string) string {
			commandWrite(t, filepath.Join(dir, "RECOVERY.md"), []byte("not a recovery map\n"), 0600)
			return "perry"
		}},
		{"unexpected file", func(t *testing.T, f *commandFixture, dir string) string {
			commandWrite(t, filepath.Join(dir, "keep.txt"), []byte("not a recovery original"), 0600)
			return "perry"
		}},
		{"nested directory", func(t *testing.T, f *commandFixture, dir string) string {
			commandWrite(t, filepath.Join(dir, "nested", "0000.jsonl"), []byte("do not recursively delete"), 0600)
			return "perry"
		}},
		{"live session storage", func(t *testing.T, f *commandFixture, dir string) string { return f.storage }},
		{"parent directory", func(t *testing.T, f *commandFixture, dir string) string { return f.agent }},
		{"outside recovery root", func(t *testing.T, f *commandFixture, dir string) string {
			return filepath.Join(f.root, "relo-recovery-111")
		}},
		{"symlink directory", func(t *testing.T, f *commandFixture, dir string) string {
			link := filepath.Join(f.agent, "relo-recovery-333")
			if err := os.Symlink(dir, link); err != nil {
				t.Fatal(err)
			}
			return link
		}},
		{"symlink map", func(t *testing.T, f *commandFixture, dir string) string {
			mapPath := filepath.Join(dir, "RECOVERY.md")
			if err := os.Remove(mapPath); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(dir, "0000.jsonl"), mapPath); err != nil {
				t.Fatal(err)
			}
			return "perry"
		}},
		{"symlink original", func(t *testing.T, f *commandFixture, dir string) string {
			if err := os.Symlink(filepath.Join(dir, "0000.jsonl"), filepath.Join(dir, "0002.jsonl")); err != nil {
				t.Fatal(err)
			}
			return "perry"
		}},
		{"storage customization", func(t *testing.T, f *commandFixture, dir string) string {
			t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(f.home, "other-agent"))
			return "perry"
		}},
		{"sessionDir setting", func(t *testing.T, f *commandFixture, dir string) string {
			commandWrite(t, filepath.Join(f.agent, "settings.json"), []byte(`{"sessionDir":"other-sessions"}`), 0600)
			return "perry"
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newCommandFixture(t)
			dir := commandRecovery(t, f, "relo-recovery-111", filepath.Join(f.root, "perry"))
			selector := tc.setup(t, f, dir)
			before := commandSnapshot(t, f.root)
			var out, diagnostic bytes.Buffer
			if code := run([]string{"cleanup", selector}, &out, &diagnostic); code != 1 || out.Len() != 0 || !strings.HasPrefix(diagnostic.String(), "Refused: ") || !strings.HasSuffix(diagnostic.String(), "No changes made\n") {
				t.Fatalf("refusal: code=%d stdout=%q stderr=%q", code, out.String(), diagnostic.String())
			}
			assertCommandSnapshot(t, f.root, before)
		})
	}
}

func TestRunCleanupDirectoryReplacementDoesNotRedirectDeletion(t *testing.T) {
	f := newCommandFixture(t)
	directory := commandRecovery(t, f, "relo-recovery-111", filepath.Join(f.root, "perry"))
	retained := filepath.Join(f.root, "retained-original-recovery")
	victim := filepath.Join(f.root, "unrelated-data")
	for _, name := range []string{"0000.jsonl", "0001.jsonl", "RECOVERY.md"} {
		commandWrite(t, filepath.Join(victim, name), []byte("unrelated live data"), 0600)
	}
	victimBefore := commandSnapshot(t, victim)
	recoveryBefore := commandSnapshot(t, directory)
	injected := false
	combined := &commandInterceptWriter{}
	combined.intercept = func(data []byte) {
		if injected || !bytes.Contains(data, []byte("Deleting recovery copies:")) {
			return
		}
		injected = true
		if err := os.Rename(directory, retained); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(victim, directory); err != nil {
			t.Fatal(err)
		}
	}
	if code := run([]string{"cleanup", "perry"}, combined, combined); code != 1 || !injected || !strings.Contains(combined.String(), "directory path changed before deletion") {
		t.Fatalf("replacement: code=%d injected=%v\n%s", code, injected, combined.String())
	}
	assertCommandSnapshot(t, victim, victimBefore)
	assertCommandSnapshot(t, retained, recoveryBefore)
	assertNoCommandReminder(t, combined.String())
}

func TestRunCleanupChangedAndPartialFailure(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(fmt.Sprintf("partial=%v", partial), func(t *testing.T) {
			f := newCommandFixture(t)
			dir := commandRecovery(t, f, "relo-recovery-111", filepath.Join(f.root, "perry"))
			sessionsBefore := commandSnapshot(t, f.storage)
			injected := false
			combined := &commandInterceptWriter{}
			combined.intercept = func(data []byte) {
				marker := "Warning: permanently removes"
				if partial {
					marker = "Deleting recovery copies:"
				}
				if injected || !bytes.Contains(data, []byte(marker)) {
					return
				}
				injected = true
				if partial {
					path := filepath.Join(dir, "0001.jsonl")
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
					commandWrite(t, filepath.Join(path, "concurrent-entry"), []byte("fixture-only deletion failure"), 0600)
				} else {
					commandWrite(t, filepath.Join(dir, "0000.jsonl"), []byte("concurrent fixture change"), 0600)
				}
			}
			if code := run([]string{"cleanup", "perry"}, combined, combined); code != 1 || !injected {
				t.Fatalf("failure: code=%d injected=%v\n%s", code, injected, combined.String())
			}
			for _, forbidden := range []string{"No changes made", "Deleted recovery directory:"} {
				if strings.Contains(combined.String(), forbidden) {
					t.Fatalf("misleading failure output:\n%s", combined.String())
				}
			}
			if !strings.Contains(combined.String(), "Cleanup failed:") {
				t.Fatal(combined.String())
			}
			if partial {
				assertCommandAbsent(t, filepath.Join(dir, "0000.jsonl"))
				if !strings.Contains(combined.String(), "removed 1 of 3 recovery files") {
					t.Fatal(combined.String())
				}
			} else if _, err := os.Stat(filepath.Join(dir, "0001.jsonl")); err != nil {
				t.Fatalf("changed plan removed an original: %v", err)
			}
			if _, err := os.Stat(filepath.Join(dir, "RECOVERY.md")); err != nil {
				t.Fatalf("failure lost recovery map: %v", err)
			}
			assertCommandSnapshot(t, f.storage, sessionsBefore)
			assertNoCommandReminder(t, combined.String())
		})
	}
}
