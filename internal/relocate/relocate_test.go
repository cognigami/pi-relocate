package relocate

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func fixtureRoot(t *testing.T) string {
	t.Helper()
	base, err := filepath.Abs("../../.fixtures")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(base, 0700); err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(base, "relocate-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	home := filepath.Join(root, "home")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	return root
}
func put(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0640); err != nil {
		t.Fatal(err)
	}
}
func fixtureCommand(t *testing.T, root, name string, args ...string) string {
	t.Helper()
	c := exec.Command(name, args...)
	c.Dir = root
	home := os.Getenv("HOME")
	c.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "XDG_CONFIG_HOME=" + home, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + filepath.Join(home, "gitconfig"), "JJ_CONFIG=" + filepath.Join(home, "jj.toml")}
	if err := os.WriteFile(filepath.Join(home, "jj.toml"), []byte("[user]\nname = 'Fixture'\nemail = 'fixture@example.invalid'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v: %s", name, args, err, out)
	}
	return strings.TrimSpace(string(out))
}
func checkoutFixture(t *testing.T, path string, jj bool) {
	t.Helper()
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
	fixtureCommand(t, path, "git", "init", ".")
	put(t, filepath.Join(path, "tracked"), []byte("committed"))
	put(t, filepath.Join(path, ".gitignore"), []byte("ignored\n"))
	fixtureCommand(t, path, "git", "add", ".")
	fixtureCommand(t, path, "git", "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-m", "fixture")
	if jj {
		fixtureCommand(t, path, "jj", "git", "init", "--colocate")
	}
	put(t, filepath.Join(path, "tracked"), []byte("dirty"))
	put(t, filepath.Join(path, "untracked"), []byte("untracked"))
	put(t, filepath.Join(path, "ignored"), []byte("ignored"))
}
func sessionFixture(t *testing.T, storage, cwd, name, id, parent string) string {
	t.Helper()
	h := map[string]any{"type": "session", "version": 3, "id": id, "timestamp": "2026-01-01T00:00:00.000Z", "cwd": cwd}
	if parent != "" {
		h["parentSession"] = parent
	}
	data, err := json.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	data = append(data, []byte(`{"type":"message","id":"entry","parentId":null,"timestamp":"2026-01-01T00:00:01Z","message":{"role":"user","content":[{"type":"text","text":"historical path stays"}],"timestamp":1767225601000}}`+"\n")...)
	path := filepath.Join(storage, BucketName(cwd), name+".jsonl")
	put(t, path, data)
	when := time.Unix(1700000000, 123456789)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
	return path
}
func basicFixture(t *testing.T, jj bool) (string, Options, string, string, string) {
	t.Helper()
	root := fixtureRoot(t)
	source := filepath.Join(root, "perry")
	destination := filepath.Join(root, "moved")
	checkoutFixture(t, source, jj)
	options := Options{Storage: filepath.Join(root, "home", ".pi", "agent", "sessions"), Caller: root, Config: filepath.Join(root, "config", "99-mini.jsonc")}
	first := sessionFixture(t, options.Storage, source, "a", "perry-a", "")
	sessionFixture(t, options.Storage, source, "b", "perry-b", first)
	external := sessionFixture(t, options.Storage, filepath.Join(root, "dtcc"), "dtcc", "dtcc", first)
	data, err := os.ReadFile(external)
	if err != nil {
		t.Fatal(err)
	}
	line, _ := json.Marshal(map[string]any{"type": "custom", "id": "bootstrap", "parentId": "entry", "customType": "pi-sessions.handoff-bootstrap", "data": map[string]any{"parentSessionFile": first, "ownerSessionId": "perry-a"}})
	put(t, external, append(data, append(line, '\n')...))
	put(t, strings.TrimSuffix(first, ".jsonl")+".sidecar.json", []byte(`{"sessionId":"perry-a","large":9007199254740993}`))
	put(t, filepath.Join(filepath.Dir(first), "orphan.sidecar.json"), []byte("orphan bytes\n"))
	put(t, options.Config, []byte(fmt.Sprintf("{\"path\":%q,\"remote\":\"remote:/unchanged\"}", source)))
	return root, options, source, destination, external
}
func TestDiscoverReadOnlyAndExecute(t *testing.T) {
	for _, jj := range []bool{false, true} {
		for _, container := range []bool{false, true} {
			t.Run(fmt.Sprintf("jj=%v/container=%v", jj, container), func(t *testing.T) {
				root, options, source, destination, external := basicFixture(t, jj)
				if container {
					destination = filepath.Join(source, "root")
				}
				before, err := inventory(root)
				if err != nil {
					t.Fatal(err)
				}
				p, err := Discover(source, destination, options)
				if err != nil {
					t.Fatal(err)
				}
				if p.Sessions != 2 || p.Sidecars != 2 || p.Orphans != 1 {
					t.Fatalf("bad counts: %d/%d/%d", p.Sessions, p.Sidecars, p.Orphans)
				}
				var preview bytes.Buffer
				p.Preview(&preview)
				if !strings.Contains(preview.String(), "External reference update: "+external) || !strings.Contains(preview.String(), "mini") {
					t.Fatal(preview.String())
				}
				after, err := inventory(root)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(before, after) {
					t.Fatal("dry run mutated fixture")
				}
				var output bytes.Buffer
				if err := p.Execute(&output); err != nil {
					t.Fatal(err)
				}
				if err := p.verify(); err != nil {
					t.Fatal(err)
				}
				for _, path := range []string{options.Config} {
					data, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					relative, _ := filepath.Rel(root, path)
					if before[relative].Hash != sha256.Sum256(data) {
						t.Fatal("configuration changed")
					}
				}
				externalData, _ := os.ReadFile(external)
				if !bytes.Contains(externalData, []byte(p.DestinationBucket)) {
					t.Fatal("external target not updated")
				}
				if !strings.Contains(output.String(), "durable metadata verified") {
					t.Fatal(output.String())
				}
			})
		}
	}
}

func TestDiscoverRefusals(t *testing.T) {
	cases := []struct {
		name   string
		change func(*testing.T, *Options, string, *string)
	}{
		{"existing-destination", func(t *testing.T, o *Options, s string, d *string) { put(t, *d, []byte("collision")) }},
		{"destination-bucket", func(t *testing.T, o *Options, s string, d *string) {
			put(t, filepath.Join(o.Storage, BucketName(*d), "collision.jsonl"), []byte("exists"))
		}},
		{"inside-caller", func(t *testing.T, o *Options, s string, d *string) { o.Caller = s }},
		{"mixed-owner-prefix", func(t *testing.T, o *Options, s string, d *string) {
			sessionFixture(t, o.Storage, s, "wrong", "wrong", "")
			p := filepath.Join(o.Storage, BucketName(s), "wrong.jsonl")
			b, _ := os.ReadFile(p)
			put(t, p, bytes.ReplaceAll(b, []byte(s), []byte(s+"-sibling")))
		}},
		{"nested-destination", func(t *testing.T, o *Options, s string, d *string) { *d = filepath.Join(s, "missing", "root") }},
		{"linked-git", func(t *testing.T, o *Options, s string, d *string) {
			if err := os.Rename(filepath.Join(s, ".git"), filepath.Join(s, "gitdata")); err != nil {
				t.Fatal(err)
			}
			put(t, filepath.Join(s, ".git"), []byte("gitdir: gitdata"))
		}},
		{"git-alternates", func(t *testing.T, o *Options, s string, d *string) {
			put(t, filepath.Join(s, ".git", "objects", "info", "alternates"), []byte("/elsewhere"))
		}},
		{"linked-jj", func(t *testing.T, o *Options, s string, d *string) {
			put(t, filepath.Join(s, ".jj", "repo"), []byte("external"))
		}},
		{"global-sessionDir", func(t *testing.T, o *Options, s string, d *string) {
			put(t, filepath.Join(filepath.Dir(o.Storage), "settings.json"), []byte(`{"sessionDir":"/custom"}`))
		}},
		{"project-sessionDir", func(t *testing.T, o *Options, s string, d *string) {
			put(t, filepath.Join(s, ".pi", "settings.json"), []byte(`{"sessionDir":"./custom"}`))
		}},
		{"malformed-header", func(t *testing.T, o *Options, s string, d *string) {
			put(t, filepath.Join(o.Storage, BucketName(s), "a.jsonl"), []byte("{invalid\n"))
		}},
		{"malformed-affected", func(t *testing.T, o *Options, s string, d *string) {
			p := filepath.Join(o.Storage, BucketName(s), "a.jsonl")
			b, _ := os.ReadFile(p)
			put(t, p, append(b, []byte(`{"type":"custom","customType":"pi-sessions.subagent_launched","data":42}`+"\n")...))
		}},
		{"duplicate-ID", func(t *testing.T, o *Options, s string, d *string) {
			sessionFixture(t, o.Storage, filepath.Join(filepath.Dir(s), "other"), "dup", "perry-a", "")
		}},
		{"symlink-destination", func(t *testing.T, o *Options, s string, d *string) {
			link := filepath.Join(filepath.Dir(s), "alias")
			if err := os.Symlink(filepath.Dir(s), link); err != nil {
				t.Fatal(err)
			}
			*d = filepath.Join(link, "new")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, o, s, d, _ := basicFixture(t, false)
			tc.change(t, &o, s, &d)
			before, err := inventory(root)
			if err != nil && tc.name != "symlink-destination" {
				t.Fatal(err)
			}
			_, err = Discover(s, d, o)
			if err == nil {
				t.Fatal("expected refusal")
			}
			after, _ := inventory(root)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("refusal mutated fixture")
			}
		})
	}
}

func TestFailureRecoveryBoundaries(t *testing.T) {
	for _, boundary := range []string{"backup", "backup-write", "backup-write-second", "checkout-staging", "checkout-staged", "session-publication", "session-publication-second", "external-reference", "verification"} {
		t.Run(boundary, func(t *testing.T) {
			_, o, s, d, _ := basicFixture(t, true)
			d = filepath.Join(s, "root")
			p, err := Discover(s, d, o)
			if err != nil {
				t.Fatal(err)
			}
			originals := map[string][]byte{}
			for _, f := range p.files {
				originals[f.Source] = f.Before
			}
			calls := 0
			p.fail = func(at string) error {
				if at == strings.TrimSuffix(boundary, "-second") {
					calls++
					if !strings.HasSuffix(boundary, "-second") || calls == 2 {
						return errors.New("injected " + at)
					}
				}
				return nil
			}
			var output bytes.Buffer
			err = p.Execute(&output)
			if err == nil {
				t.Fatal("expected failure")
			}
			if strings.Contains(output.String(), "durable metadata verified") || strings.Contains(output.String(), "Reminder:") {
				t.Fatal(output.String())
			}
			if !strings.Contains(err.Error(), "Checkout location:") || !strings.Contains(err.Error(), "Do not blindly rerun") {
				t.Fatal(err)
			}
			locations := []string{s, d}
			stages, _ := filepath.Glob(filepath.Join(filepath.Dir(s), ".relo-*"))
			locations = append(locations, stages...)
			found := false
			for _, location := range locations {
				if unchanged(location, p.checkout) == nil {
					found = true
				}
			}
			if !found {
				t.Fatal("checkout no longer recoverable")
			}
			recoveries, _ := filepath.Glob(filepath.Join(filepath.Dir(o.Storage), "relo-recovery-*"))
			for path, original := range originals {
				found = false
				for _, candidate := range []string{path, strings.Replace(path, p.SourceBucket, p.DestinationBucket, 1)} {
					data, _ := os.ReadFile(candidate)
					if bytes.Equal(data, original) {
						found = true
					}
				}
				for _, recovery := range recoveries {
					backups, _ := filepath.Glob(filepath.Join(recovery, "*.jsonl"))
					for _, backup := range backups {
						data, _ := os.ReadFile(backup)
						if bytes.Equal(data, original) {
							found = true
						}
					}
				}
				if !found {
					t.Fatalf("original metadata not recoverable: %s", path)
				}
			}
		})
	}
}
func TestVerificationDetectsCorruption(t *testing.T) {
	_, o, s, d, _ := basicFixture(t, false)
	p, err := Discover(s, d, o)
	if err != nil {
		t.Fatal(err)
	}
	p.fail = func(at string) error {
		if at == "verification" {
			put(t, filepath.Join(d, "tracked"), []byte("corrupted"))
		}
		return nil
	}
	if err := p.Execute(&bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "inventory mismatch") {
		t.Fatalf("expected verification error, got %v", err)
	}
}
func TestPathEncoding(t *testing.T) {
	if got := BucketName("/a/b:c\\d"); got != "--a-b-c-d--" {
		t.Fatal(got)
	}
	if within("/perry-other", "/perry") {
		t.Fatal("prefix is not ancestry")
	}
}
func TestStorageEnvironmentRefused(t *testing.T) {
	fixtureRoot(t)
	for _, key := range []string{"PI_CODING_AGENT_DIR", "PI_CODING_AGENT_SESSION_DIR"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, "custom")
			if _, err := DefaultOptions(); err == nil {
				t.Fatal("expected refusal")
			}
		})
	}
}
