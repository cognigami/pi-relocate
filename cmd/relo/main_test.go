package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRunHelpAndUsage(t *testing.T) {
	var out, diagnostic bytes.Buffer
	if code := run([]string{"--help"}, &out, &diagnostic); code != 0 || out.String() != usage || diagnostic.Len() != 0 {
		t.Fatalf("help: code=%d stdout=%q stderr=%q", code, out.String(), diagnostic.String())
	}
	for _, args := range [][]string{
		nil, {""}, {"source"}, {"source", "destination", "extra"},
		{"", "destination"}, {"source", ""}, {"--unknown", "destination"},
		{"source", "--dry-run"}, {"--help", "destination"}, {"--dry-run"},
		{"--dry-run", "--help"}, {"--dry-run", "source", "destination", "extra"},
		{"--dry-run", "-source", "destination"}, {"--", "source", "destination"},
	} {
		t.Run(fmt.Sprintf("%q", args), func(t *testing.T) {
			var out, diagnostic bytes.Buffer
			if code := run(args, &out, &diagnostic); code != 1 || out.Len() != 0 || diagnostic.String() != usage {
				t.Fatalf("usage: code=%d stdout=%q stderr=%q", code, out.String(), diagnostic.String())
			}
		})
	}
}

func TestRunDryRunDoesNotWrite(t *testing.T) {
	f := newCommandFixture(t)
	source := f.checkout(t, "source checkout")
	destination := filepath.Join(f.root, "new parent", "relocated checkout")
	collection := f.collection(t, source)
	before := commandSnapshot(t, f.root)
	var out, diagnostic bytes.Buffer
	code := run([]string{"--dry-run", "source checkout", filepath.Join("new parent", "relocated checkout")}, &out, &diagnostic)
	if code != 0 || diagnostic.Len() != 0 {
		t.Fatalf("dry-run: code=%d stdout=%s stderr=%s", code, &out, &diagnostic)
	}
	for _, text := range []string{source, destination, collection.external, "2 sessions", "2 sidecars", "1 orphans", f.config, "Preview validated. No changes made."} {
		if !strings.Contains(out.String(), text) {
			t.Errorf("preview lacks %q:\n%s", text, &out)
		}
	}
	assertNoCommandReminder(t, out.String())
	if strings.Contains(out.String(), "HISTORICAL FIXTURE") {
		t.Error("preview disclosed conversation content")
	}
	assertCommandSnapshot(t, f.root, before)
}

func TestRunRelocatesRelativePathsWithSpaces(t *testing.T) {
	f := newCommandFixture(t)
	source := f.checkout(t, "source checkout")
	destination := filepath.Join(f.root, "new parent", "relocated checkout")
	collection := f.collection(t, source)
	checkoutBefore := commandSnapshot(t, source)
	homeBefore := f.protectedHome(t)
	var combined bytes.Buffer
	if code := run([]string{"./source checkout", filepath.Join("new parent", "relocated checkout")}, &combined, &combined); code != 0 {
		t.Fatalf("relocation: code=%d\n%s", code, &combined)
	}
	assertCommandSuccess(t, combined.String(), destination)
	assertCommandAbsent(t, source)
	assertCommandSnapshot(t, destination, checkoutBefore)
	collection.assertMoved(t, f, destination)
	f.assertProtectedHome(t, homeBefore)
	f.assertRecovery(t, collection.originals)
	if !strings.Contains(combined.String(), f.config) || !strings.Contains(combined.String(), "interactive acceptance is pending") {
		t.Fatalf("missing configuration/acceptance disclosure:\n%s", &combined)
	}
}

func TestRunTwoStepFamilyLayout(t *testing.T) {
	f := newCommandFixture(t)
	perry := f.checkout(t, "perry")
	engine := f.checkout(t, "engine1")
	collection := f.collection(t, perry)
	engineID := "55555555-5555-4555-8555-555555555555"
	engineFile := filepath.Join(f.bucket(engine), "2025-01-02T03-04-05-000Z_"+engineID+".jsonl")
	engineData := commandJSONL(t, commandHeader(engineID, engine, ""), commandMessage("engine-message", "HISTORICAL FIXTURE "+engine))
	commandWrite(t, engineFile, engineData, 0600)
	perryBefore, engineBefore := commandSnapshot(t, perry), commandSnapshot(t, engine)
	engineSessionsBefore := commandSnapshot(t, f.bucket(engine))
	homeBefore := f.protectedHome(t)
	root := filepath.Join(perry, "root")
	var combined bytes.Buffer
	beforePreview := commandSnapshot(t, f.root)
	if code := run([]string{"--dry-run", "perry", filepath.Join("perry", "root")}, &combined, &combined); code != 0 {
		t.Fatalf("family preview: code=%d\n%s", code, &combined)
	}
	assertNoCommandReminder(t, combined.String())
	if !strings.Contains(combined.String(), collection.external) {
		t.Fatal("family preview omitted external DTCC reference")
	}
	assertCommandSnapshot(t, f.root, beforePreview)
	combined.Reset()
	if code := run([]string{"perry", filepath.Join("perry", "root")}, &combined, &combined); code != 0 {
		t.Fatalf("first relocation: code=%d\n%s", code, &combined)
	}
	assertCommandSuccess(t, combined.String(), root)
	assertCommandSnapshot(t, root, perryBefore)
	assertCommandAbsent(t, filepath.Join(perry, ".git"))
	collection.assertMoved(t, f, root)
	assertCommandSnapshot(t, engine, engineBefore)
	assertCommandSnapshot(t, f.bucket(engine), engineSessionsBefore)
	rootBucketBefore := commandSnapshot(t, f.bucket(root))
	externalBefore := commandRead(t, collection.external)
	combined.Reset()
	engineDestination := filepath.Join(perry, "engine1")
	// Absolute operands complement the relative operands in the first invocation.
	if code := run([]string{engine, engineDestination}, &combined, &combined); code != 0 {
		t.Fatalf("second relocation: code=%d\n%s", code, &combined)
	}
	assertCommandSuccess(t, combined.String(), engineDestination)
	assertCommandAbsent(t, engine)
	assertCommandAbsent(t, f.bucket(engine))
	assertCommandSnapshot(t, engineDestination, engineBefore)
	assertCommandSnapshot(t, root, perryBefore)
	assertCommandSnapshot(t, f.bucket(root), rootBucketBefore)
	if !bytes.Equal(commandRead(t, collection.external), externalBefore) {
		t.Fatal("second relocation changed the DTCC session pointing into Perry/root")
	}
	newEngineFile := filepath.Join(f.bucket(engineDestination), filepath.Base(engineFile))
	assertCommandJSONL(t, commandRead(t, newEngineFile), commandJSONL(t, commandHeader(engineID, engineDestination, ""), commandMessage("engine-message", "HISTORICAL FIXTURE "+engine)))
	f.assertProtectedHome(t, homeBefore)
	entries, err := os.ReadDir(perry)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Name() != "engine1" || entries[1].Name() != "root" {
		t.Fatalf("old Perry must be only a family container: %v", entries)
	}
}

func TestRunRefusalMakesNoChanges(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*testing.T, *commandFixture, string, string, *commandCollection)
	}{
		{"existing destination", func(t *testing.T, f *commandFixture, source, destination string, c *commandCollection) {
			if err := os.Mkdir(destination, 0700); err != nil {
				t.Fatal(err)
			}
		}},
		{"existing destination bucket", func(t *testing.T, f *commandFixture, source, destination string, c *commandCollection) {
			commandWrite(t, filepath.Join(f.bucket(destination), "sentinel"), []byte("do not overwrite"), 0600)
		}},
		{"duplicate session ID", func(t *testing.T, f *commandFixture, source, destination string, c *commandCollection) {
			commandWrite(t, filepath.Join(f.bucket(filepath.Join(f.root, "duplicate")), "duplicate.jsonl"), commandJSONL(t, commandHeader(commandParentID, filepath.Join(f.root, "duplicate"), "")), 0600)
		}},
		{"malformed affected session", func(t *testing.T, f *commandFixture, source, destination string, c *commandCollection) {
			commandWrite(t, c.parent, []byte("{not JSON}\n"), 0600)
		}},
		{"mixed bucket ownership", func(t *testing.T, f *commandFixture, source, destination string, c *commandCollection) {
			commandWrite(t, c.parent, commandJSONL(t, commandHeader(commandParentID, source+"-other", "")), 0600)
		}},
		{"linked Git layout", func(t *testing.T, f *commandFixture, source, destination string, c *commandCollection) {
			commandWrite(t, filepath.Join(source, ".git", "commondir"), []byte("../outside\n"), 0600)
		}},
		{"caller inside source", func(t *testing.T, f *commandFixture, source, destination string, c *commandCollection) {
			t.Chdir(source)
		}},
	}
	for _, key := range []string{"PI_CODING_AGENT_DIR", "PI_CODING_AGENT_SESSION_DIR"} {
		cases = append(cases, struct {
			name  string
			setup func(*testing.T, *commandFixture, string, string, *commandCollection)
		}{key, func(t *testing.T, f *commandFixture, source, destination string, c *commandCollection) {
			t.Setenv(key, filepath.Join(f.home, "unsupported custom storage"))
		}})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newCommandFixture(t)
			source := f.checkout(t, "perry")
			destination := filepath.Join(f.root, "destination")
			collection := f.collection(t, source)
			tc.setup(t, f, source, destination, collection)
			before := commandSnapshot(t, f.root)
			var out, diagnostic bytes.Buffer
			if code := run([]string{source, destination}, &out, &diagnostic); code != 1 {
				t.Fatalf("refusal code=%d stdout=%s stderr=%s", code, &out, &diagnostic)
			}
			if out.Len() != 0 || !strings.HasPrefix(diagnostic.String(), "Refused: ") || !strings.HasSuffix(diagnostic.String(), "\nNo changes made\n") {
				t.Fatalf("incorrect refusal output: stdout=%q stderr=%q", out.String(), diagnostic.String())
			}
			assertNoCommandReminder(t, out.String()+diagnostic.String())
			assertCommandSnapshot(t, f.root, before)
		})
	}
}

func TestRunFailureAfterRecoveryDoesNotClaimNoChanges(t *testing.T) {
	f := newCommandFixture(t)
	source := f.checkout(t, "perry")
	destination := filepath.Join(f.root, "destination")
	collection := f.collection(t, source)
	checkoutBefore, sessionsBefore := commandSnapshot(t, source), commandSnapshot(t, f.storage)
	// Introduce a fixture-only race after validation and backup, immediately before
	// the checkout rename. A nonempty destination makes rename fail portably.
	injected := false
	combined := &commandInterceptWriter{}
	combined.intercept = func(data []byte) {
		if !injected && bytes.Contains(data, []byte("Moving checkout...")) {
			injected = true
			commandWrite(t, filepath.Join(destination, "collision"), []byte("concurrent fixture file"), 0600)
		}
	}
	if code := run([]string{source, destination}, combined, combined); code != 1 {
		t.Fatalf("failed relocation code=%d\n%s", code, combined.String())
	}
	if !injected {
		t.Fatal("fixture failure was not injected")
	}
	for _, text := range []string{"Relocation failed:", "Recovery copies:", "Checkout location: " + source, "Do not blindly rerun"} {
		if !strings.Contains(combined.String(), text) {
			t.Errorf("partial failure lacks %q:\n%s", text, combined.String())
		}
	}
	if strings.Contains(combined.String(), "No changes made") {
		t.Fatal("failure after creating recovery copies falsely claimed no changes")
	}
	assertNoCommandReminder(t, combined.String())
	assertCommandSnapshot(t, source, checkoutBefore)
	assertCommandSnapshot(t, f.storage, sessionsBefore)
	f.assertRecovery(t, collection.originals)
}

func TestRunInstalledPiSessionDiscovery(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("installed Pi compatibility unavailable: node: %v", err)
	}
	module := installedCommandPiModule(t)
	f := newCommandFixture(t)
	source := f.checkout(t, "Pi compatible checkout")
	destination := filepath.Join(f.root, "Pi relocated checkout")
	collection := f.collection(t, source)
	lookup := func(cwd, parent, child string) {
		t.Helper()
		expected, err := json.Marshal([]map[string]string{
			{"id": commandParentID, "path": parent, "cwd": cwd},
			{"id": commandChildID, "path": child, "cwd": cwd, "parentSessionPath": parent},
		})
		if err != nil {
			t.Fatal(err)
		}
		const script = `
import assert from 'node:assert/strict';
import { dirname } from 'node:path';
import { pathToFileURL } from 'node:url';
const [modulePath, cwd, agentDir, expectedJSON] = process.argv.slice(1);
const { SessionManager, getDefaultSessionDir } = await import(pathToFileURL(modulePath).href);
const expected = JSON.parse(expectedJSON);
const bucket = getDefaultSessionDir(cwd, agentDir);
assert.equal(bucket, dirname(expected[0].path));
const listed = await SessionManager.list(cwd, bucket);
assert.deepEqual(listed.map(s => s.id).sort(), expected.map(s => s.id).sort());
for (const item of expected) {
  assert.equal(SessionManager.findById(cwd, item.id, bucket), item.path);
  const found = listed.find(s => s.id === item.id);
  assert.equal(found.path, item.path);
  assert.equal(found.cwd, item.cwd);
  if (item.parentSessionPath) assert.equal(found.parentSessionPath, item.parentSessionPath);
}
console.log('Fixture-only installed Pi SessionManager discovery verified.');
`
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, node, "--input-type=module", "--eval", script, module, cwd, f.agent, string(expected))
		cmd.Dir = f.root
		// The override is intentionally confined to this Node fixture subprocess;
		// the relo command itself must use HOME's default storage.
		cmd.Env = append(f.childEnv(), "PI_CODING_AGENT_DIR="+f.agent)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("installed Pi fixture discovery: %v\n%s", err, output)
		} else {
			t.Log(strings.TrimSpace(string(output)))
		}
	}
	lookup(source, collection.parent, collection.child)
	var combined bytes.Buffer
	if code := run([]string{source, destination}, &combined, &combined); code != 0 {
		t.Fatalf("relocation: code=%d\n%s", code, &combined)
	}
	assertCommandSuccess(t, combined.String(), destination)
	collection.assertMoved(t, f, destination)
	lookup(destination, filepath.Join(f.bucket(destination), filepath.Base(collection.parent)), filepath.Join(f.bucket(destination), filepath.Base(collection.child)))
}

// No test runs in parallel: run uses process HOME and cwd. All writable paths,
// including temporary repositories and default Pi storage, are below .fixtures.
type commandFixture struct {
	root, home, agent, storage, config string
}

func newCommandFixture(t *testing.T) *commandFixture {
	t.Helper()
	base, err := filepath.Abs("../../.fixtures")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(base, 0700); err != nil {
		t.Fatal(err)
	}
	base, err = filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(base, "command-")
	if err != nil {
		t.Fatal(err)
	}
	// Register first so environment/cwd cleanups happen before removing fixtures.
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	f := &commandFixture{root: root, home: filepath.Join(root, "home")}
	f.agent = filepath.Join(f.home, ".pi", "agent")
	f.storage = filepath.Join(f.agent, "sessions")
	f.config = filepath.Join(f.home, ".config", "pi-extensions", "sandbox", "99-mini.jsonc")
	if err := os.MkdirAll(f.storage, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", f.home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(f.home, ".config"))
	for _, key := range []string{"PI_CODING_AGENT_DIR", "PI_CODING_AGENT_SESSION_DIR", "PI_AGENT_DIR"} {
		t.Setenv(key, "")
	}
	t.Chdir(root)
	// These are synthetic protected-state sentinels, not assertions about any
	// particular installed extension's private storage layout.
	commandWrite(t, filepath.Join(f.agent, "fixture-index", "index.sqlite"), []byte("fixture index; never rebuild automatically\x00"), 0600)
	commandWrite(t, filepath.Join(f.agent, "fixture-messaging", "broker-state"), []byte("fixture messaging state; never migrate"), 0600)
	commandWrite(t, filepath.Join(f.agent, "settings.json"), []byte("{\"fixtureSetting\":true}\n"), 0600)
	commandWrite(t, f.config, []byte("// External mini configuration; keep remote path unchanged.\n{}\n"), 0600)
	return f
}

func (f *commandFixture) childEnv() []string {
	return []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + f.home,
		"XDG_CONFIG_HOME=" + filepath.Join(f.home, ".config"),
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + filepath.Join(f.home, "absent-gitconfig"),
		"GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid",
		"GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid",
		"GIT_TERMINAL_PROMPT=0",
	}
}

func (f *commandFixture) checkout(t *testing.T, name string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("fixture Git unavailable: %v", err)
	}
	source := filepath.Join(f.root, name)
	git := func(at string, args ...string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		args = append([]string{"-c", "commit.gpgsign=false", "-c", "core.hooksPath=" + filepath.Join(f.home, "absent-hooks")}, args...)
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir, cmd.Env = at, f.childEnv()
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fixture git %v: %v\n%s", args, err, output)
		}
	}
	git(f.root, "init", "--template=", "--initial-branch=fixture", source)
	commandWrite(t, filepath.Join(source, "tracked"), []byte("committed\n"), 0644)
	commandWrite(t, filepath.Join(source, ".gitignore"), []byte("ignored\n"), 0644)
	git(source, "add", "tracked", ".gitignore")
	git(source, "commit", "-m", "fixture only")
	commandWrite(t, filepath.Join(source, "tracked"), []byte("dirty tracked bytes\n"), 0644)
	commandWrite(t, filepath.Join(source, "untracked file"), []byte("untracked\n"), 0644)
	commandWrite(t, filepath.Join(source, "ignored"), []byte("ignored\n"), 0600)
	commandWrite(t, filepath.Join(source, "executable"), []byte("fixture executable bytes\n"), 0755)
	return source
}

func (f *commandFixture) bucket(cwd string) string {
	// Independent expectation of Pi's documented directory encoding; installed
	// Pi compatibility is also tested rather than trusting only this formula.
	encoded := strings.NewReplacer("/", "-", "\\", "-", ":", "-").Replace(strings.TrimLeft(cwd, "/\\"))
	return filepath.Join(f.storage, "--"+encoded+"--")
}

const (
	commandParentID = "11111111-1111-4111-8111-111111111111"
	commandChildID  = "22222222-2222-4222-8222-222222222222"
	commandDTCCID   = "33333333-3333-4333-8333-333333333333"
)

type commandCollection struct {
	source, externalCwd, parent, child, external string
	originals                                    map[string][]byte
	states                                       map[string]commandFileState
	unchanged                                    map[string]map[string]commandFileState
}

func (f *commandFixture) collection(t *testing.T, source string) *commandCollection {
	t.Helper()
	c := &commandCollection{source: source, externalCwd: filepath.Join(f.root, "dtcc"), unchanged: make(map[string]map[string]commandFileState)}
	fileName := func(id string) string { return "2025-01-02T03-04-05-000Z_" + id + ".jsonl" }
	c.parent = filepath.Join(f.bucket(source), fileName(commandParentID))
	c.child = filepath.Join(f.bucket(source), fileName(commandChildID))
	c.external = filepath.Join(f.bucket(c.externalCwd), fileName(commandDTCCID))
	c.originals = c.documents(t, source, c.parent, c.child)
	for path, data := range c.originals {
		commandWrite(t, path, data, 0600)
	}
	commandWrite(t, strings.TrimSuffix(c.parent, ".jsonl")+".sidecar.json", []byte("{\"tree\":\"keep these navigation IDs\"}\n"), 0600)
	commandWrite(t, filepath.Join(f.bucket(source), "orphan.sidecar.json"), []byte("{\"orphan\":true}\n"), 0640)
	for i, cwd := range []string{source + "-other", filepath.Join(source, "nested")} {
		id := fmt.Sprintf("66666666-6666-4666-8666-%012d", i)
		commandWrite(t, filepath.Join(f.bucket(cwd), fileName(id)), commandJSONL(t, commandHeader(id, cwd, ""), commandMessage("unrelated", "HISTORICAL FIXTURE "+source)), 0600)
		c.unchanged[f.bucket(cwd)] = commandSnapshot(t, f.bucket(cwd))
	}
	config, err := json.Marshal(map[string]any{"fixtureSource": source, "remoteWorkingDirectory": "/remote/not-the-local-checkout"})
	if err != nil {
		t.Fatal(err)
	}
	commandWrite(t, f.config, append(config, '\n'), 0600)
	c.states = commandSnapshot(t, f.storage)
	return c
}

func (c *commandCollection) documents(t *testing.T, cwd, parent, child string) map[string][]byte {
	t.Helper()
	custom := func(id, parentID, kind string, data any) map[string]any {
		return map[string]any{"type": "custom", "id": id, "parentId": parentID, "timestamp": "2025-01-02T03:04:06.000Z", "customType": kind, "data": data}
	}
	return map[string][]byte{
		parent: commandJSONL(t, commandHeader(commandParentID, cwd, ""),
			commandMessage("parent-message", "HISTORICAL FIXTURE "+c.source+" "+c.parent),
			map[string]any{"type": "session_info", "id": "name-entry", "parentId": "parent-message", "timestamp": "2025-01-02T03:04:06.000Z", "name": "Fixture parent"},
			custom("launch", "parent-message", "pi-sessions.subagent_launched", map[string]any{"childSessionFile": child, "cwd": cwd, "childSessionId": commandChildID, "ownerSessionId": commandParentID}),
			custom("history", "parent-message", "pi-sessions.message_received", map[string]any{"from": map[string]any{"sessionFile": c.child, "cwd": c.source}, "text": "HISTORICAL FIXTURE"})),
		child: commandJSONL(t, commandHeader(commandChildID, cwd, parent),
			commandMessage("child-message", "HISTORICAL FIXTURE "+c.source),
			custom("child-bootstrap", "child-message", "pi-sessions.handoff-bootstrap", map[string]any{"parentSessionFile": parent, "parentSessionId": commandParentID}),
			custom("unknown", "child-message", "unrelated-extension", map[string]any{"cwd": c.source, "parentSessionFile": c.parent})),
		c.external: commandJSONL(t, commandHeader(commandDTCCID, c.externalCwd, parent),
			commandMessage("dtcc-message", "HISTORICAL FIXTURE "+c.parent),
			custom("dtcc-bootstrap", "dtcc-message", "pi-sessions.handoff-bootstrap", map[string]any{"parentSessionFile": parent, "parentSessionId": commandParentID, "ownerSessionId": commandDTCCID})),
	}
}

func (c *commandCollection) assertMoved(t *testing.T, f *commandFixture, destination string) {
	t.Helper()
	assertCommandAbsent(t, f.bucket(c.source))
	parent := filepath.Join(f.bucket(destination), filepath.Base(c.parent))
	child := filepath.Join(f.bucket(destination), filepath.Base(c.child))
	for path, expected := range c.documents(t, destination, parent, child) {
		assertCommandJSONL(t, commandRead(t, path), expected)
		original := path
		if path == parent {
			original = c.parent
		} else if path == child {
			original = c.child
		}
		rel, err := filepath.Rel(f.storage, original)
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if state := c.states[rel]; info.Mode() != state.mode || info.ModTime().UnixNano() != state.mtime {
			t.Errorf("session mode/recency changed: %s", path)
		}
		// Historical and unknown records must retain their original line bytes.
		for _, line := range bytes.Split(c.originals[original], []byte{'\n'}) {
			if bytes.Contains(line, []byte("HISTORICAL FIXTURE")) || bytes.Contains(line, []byte("unrelated-extension")) {
				if !bytes.Contains(commandRead(t, path), append(append([]byte(nil), line...), '\n')) {
					t.Errorf("historical/unknown line changed in %s", path)
				}
			}
		}
	}
	for _, name := range []string{strings.TrimSuffix(filepath.Base(c.parent), ".jsonl") + ".sidecar.json", "orphan.sidecar.json"} {
		oldRel, err := filepath.Rel(f.storage, filepath.Join(f.bucket(c.source), name))
		if err != nil {
			t.Fatal(err)
		}
		actual := commandSnapshot(t, f.bucket(destination))[name]
		if actual != c.states[oldRel] {
			t.Errorf("sidecar changed: %s", name)
		}
	}
	entries, err := os.ReadDir(f.bucket(destination))
	if err != nil || len(entries) != 4 {
		t.Fatalf("destination collection: entries=%v error=%v", entries, err)
	}
	for bucket, before := range c.unchanged {
		assertCommandSnapshot(t, bucket, before)
	}
}

func commandHeader(id, cwd, parent string) map[string]any {
	header := map[string]any{"type": "session", "version": 3, "id": id, "timestamp": "2025-01-02T03:04:05.000Z", "cwd": cwd}
	if parent != "" {
		header["parentSession"] = parent
	}
	return header
}

func commandMessage(id, text string) map[string]any {
	return map[string]any{"type": "message", "id": id, "parentId": nil, "timestamp": "2025-01-02T03:04:06.000Z", "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": text}}, "timestamp": int64(1735787046000)}}
}

func commandJSONL(t *testing.T, records ...map[string]any) []byte {
	t.Helper()
	var data bytes.Buffer
	for _, record := range records {
		if err := json.NewEncoder(&data).Encode(record); err != nil {
			t.Fatal(err)
		}
	}
	return data.Bytes()
}

func assertCommandJSONL(t *testing.T, actual, expected []byte) {
	t.Helper()
	decode := func(data []byte) []any {
		t.Helper()
		var records []any
		for _, line := range bytes.Split(bytes.TrimSuffix(data, []byte{'\n'}), []byte{'\n'}) {
			decoder := json.NewDecoder(bytes.NewReader(line))
			decoder.UseNumber()
			var record any
			if err := decoder.Decode(&record); err != nil {
				t.Fatal(err)
			}
			records = append(records, record)
		}
		return records
	}
	if !reflect.DeepEqual(decode(actual), decode(expected)) {
		t.Fatalf("session differs beyond expected location updates:\nactual=%s\nexpected=%s", actual, expected)
	}
}

type commandFileState struct {
	hash  [32]byte
	mode  os.FileMode
	mtime int64
	link  string
}

func commandSnapshot(t *testing.T, root string) map[string]commandFileState {
	t.Helper()
	states := make(map[string]commandFileState)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		state := commandFileState{mode: info.Mode(), mtime: info.ModTime().UnixNano()}
		if info.Mode()&os.ModeSymlink != 0 {
			state.link, err = os.Readlink(path)
		} else if info.Mode().IsRegular() {
			var data []byte
			data, err = os.ReadFile(path)
			state.hash = sha256.Sum256(data)
		}
		states[rel] = state
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return states
}

func assertCommandSnapshot(t *testing.T, root string, before map[string]commandFileState) {
	t.Helper()
	if after := commandSnapshot(t, root); !reflect.DeepEqual(before, after) {
		for path, state := range before {
			if after[path] != state {
				t.Errorf("changed/missing fixture entry: %s", filepath.Join(root, path))
			}
		}
		for path := range after {
			if _, ok := before[path]; !ok {
				t.Errorf("unexpected fixture entry: %s", filepath.Join(root, path))
			}
		}
		t.Fatal("fixture inventory changed")
	}
}

func (f *commandFixture) protectedHome(t *testing.T) map[string]commandFileState {
	t.Helper()
	states := commandSnapshot(t, f.home)
	for path, state := range states {
		parts := strings.Split(filepath.ToSlash(path), "/")
		if len(parts) >= 3 && parts[0] == ".pi" && parts[1] == "agent" && (parts[2] == "sessions" || strings.HasPrefix(parts[2], "relo-recovery-")) {
			delete(states, path)
			continue
		}
		// Creating recovery siblings necessarily changes the agent directory time.
		if path == filepath.Join(".pi", "agent") {
			state.mtime = 0
			states[path] = state
		}
	}
	return states
}

func (f *commandFixture) assertProtectedHome(t *testing.T, before map[string]commandFileState) {
	t.Helper()
	if !reflect.DeepEqual(before, f.protectedHome(t)) {
		t.Fatal("relocation wrote outside default sessions and recovery artifacts (configuration/index/messaging must remain untouched)")
	}
}

func (f *commandFixture) assertRecovery(t *testing.T, originals map[string][]byte) {
	t.Helper()
	entries, err := os.ReadDir(f.agent)
	if err != nil {
		t.Fatal(err)
	}
	found := make(map[string]bool)
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "relo-recovery-") {
			continue
		}
		recovery := filepath.Join(f.agent, entry.Name())
		manifest := commandRead(t, filepath.Join(recovery, "RECOVERY.md"))
		files, err := os.ReadDir(recovery)
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			if strings.HasSuffix(file.Name(), ".jsonl") {
				data := commandRead(t, filepath.Join(recovery, file.Name()))
				for original, expected := range originals {
					if bytes.Equal(data, expected) && bytes.Contains(manifest, []byte(original)) {
						found[original] = true
					}
				}
			}
		}
	}
	for original := range originals {
		if !found[original] {
			t.Errorf("missing recoverable original and recovery mapping: %s", original)
		}
	}
}

func commandWrite(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
}

func commandRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func assertCommandAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("expected absent path %s, got %v", path, err)
	}
}

func assertCommandSuccess(t *testing.T, output, destination string) {
	t.Helper()
	reminder := "Reminder: start Pi in " + destination + " and use /session-index to rebuild the session index.\n"
	if !strings.HasSuffix(output, reminder) || strings.Count(output, "Reminder: start Pi in ") != 1 {
		t.Fatalf("success reminder is not the last shared stdout/stderr output:\n%s", output)
	}
	if strings.Contains(output, "No changes made") || strings.Contains(output, "Relocation failed:") || strings.Contains(output, "Refused:") {
		t.Fatalf("success contains failure/preview output:\n%s", output)
	}
}

func assertNoCommandReminder(t *testing.T, output string) {
	t.Helper()
	if strings.Contains(output, "Reminder: start Pi in ") {
		t.Fatalf("non-success printed the success reminder:\n%s", output)
	}
}

type commandInterceptWriter struct {
	bytes.Buffer
	intercept func([]byte)
}

func (w *commandInterceptWriter) Write(data []byte) (int, error) {
	w.intercept(data)
	return w.Buffer.Write(data)
}

func installedCommandPiModule(t *testing.T) string {
	t.Helper()
	pi, err := exec.LookPath("pi")
	if err != nil {
		t.Skipf("installed Pi compatibility unavailable: pi: %v", err)
	}
	pi, err = filepath.EvalSymlinks(pi)
	if err != nil {
		t.Skipf("installed Pi compatibility unavailable: resolve pi: %v", err)
	}
	for dir := filepath.Dir(pi); ; dir = filepath.Dir(dir) {
		data, err := os.ReadFile(filepath.Join(dir, "package.json"))
		if err == nil {
			var pkg struct {
				Name string `json:"name"`
			}
			if json.Unmarshal(data, &pkg) == nil && (pkg.Name == "pi-coding-agent" || strings.HasSuffix(pkg.Name, "/pi-coding-agent")) {
				module := filepath.Join(dir, "dist", "core", "session-manager.js")
				if info, err := os.Stat(module); err == nil && info.Mode().IsRegular() {
					return module
				}
				t.Skipf("installed Pi compatibility unavailable: %s has no dist/core/session-manager.js", pkg.Name)
			}
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	t.Skipf("installed Pi compatibility unavailable: no pi-coding-agent package above resolved executable %s", pi)
	return ""
}
