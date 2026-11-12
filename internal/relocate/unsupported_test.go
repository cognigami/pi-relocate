package relocate

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestUnsupportedOperationalDependencies(t *testing.T) {
	mapping := map[string]string{"/sessions/old/a.jsonl": "/sessions/new/a.jsonl"}
	cases := []struct {
		line    string
		blocked bool
	}{
		{`{"type":"custom","customType":"pi-sessions.lineage","data":{"parentSessionFile":"/sessions/old/a.jsonl"}}`, true},
		{`{"type":"custom","customType":"pi-sessions.lineage","data":{"parentSessionFile":"/unrelated/a.jsonl"}}`, false},
		{`{"type":"message","message":{"role":"toolResult","toolName":"session_handoff","details":{"childSessionFile":"/sessions/old/a.jsonl","launch":"deferred"}}}`, true},
		{`{"type":"message","message":{"role":"toolResult","toolName":"subagent_launch","details":{"childSessionFile":"/sessions/old/a.jsonl","launch":"subagent"}}}`, false},
		{`{"type":"message","message":{"role":"toolResult","toolName":"subagent_launch","details":{"childSessionFile":"/sessions/old/a.jsonl"}}}`, false},
		{`{"type":"custom","customType":"pi-sessions.subagent_launched","data":{"childSessionFile":"/sessions/old/a.jsonl","cwd":"/old","resumeCommand":"cd /old && pi --session-id example"}}`, true},
		{`{"type":"custom","customType":"pi-sessions.subagent_launched","data":{"childSessionFile":"/sessions/old/a.jsonl","cwd":"/old","resumeCommand":"pi --session-dir /sessions/old --session-id example"}}`, true},
		{`{"type":"custom","customType":"pi-sessions.subagent_launched","data":{"childSessionFile":"/sessions/old/a.jsonl","cwd":"/old","resumeCommand":"pi --session-id example"}}`, false},
		{`{"type":"custom","customType":"historical-research","data":{"parentSessionFile":"/sessions/old/a.jsonl"}}`, false},
	}
	for _, tc := range cases {
		err := unsupportedReference([]byte(tc.line), "/old", mapping)
		if (err != nil) != tc.blocked {
			t.Errorf("blocked=%v expected=%v: %s; err=%v", err != nil, tc.blocked, tc.line, err)
		}
	}
}
func TestRelativeDiscoveryAndSymlinkDotDotRefusal(t *testing.T) {
	root, o, s, _, _ := basicFixture(t, false)
	p, err := Discover("perry", "missing/new", o)
	if err != nil {
		t.Fatal(err)
	}
	if p.Source != s || p.Destination != filepath.Join(root, "missing", "new") {
		t.Fatal("wrong relative resolution")
	}
	link := filepath.Join(root, "alias")
	if err := os.Symlink(s, link); err != nil {
		t.Fatal(err)
	}
	_, err = Discover(link+"/../perry", filepath.Join(root, "new"), o)
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink/.. input should refuse: %v", err)
	}
}
func TestCrossDeviceRefusedBeforeWrites(t *testing.T) {
	root, o, s, _, _ := basicFixture(t, false)
	sourceInfo, err := os.Stat(s)
	if err != nil {
		t.Fatal(err)
	}
	rootInfo, err := os.Stat("/")
	if err != nil {
		t.Fatal(err)
	}
	if sourceInfo.Sys().(*syscall.Stat_t).Dev == rootInfo.Sys().(*syscall.Stat_t).Dev {
		t.Skip("fixture and / share a device; no second mounted device available for this read-only refusal test")
	}
	before, err := inventory(root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Discover(s, filepath.Join("/", filepath.Base(root)+"-destination"), o)
	if err == nil || !strings.Contains(err.Error(), "cross-device") {
		t.Fatalf("expected pre-write cross-device refusal: %v", err)
	}
	if err := unchanged(root, before); err != nil {
		t.Fatal(err)
	}
}
func TestActualLegacyFieldsRemainUnchanged(t *testing.T) {
	input := []byte(`{"type":"custom","customType":"pi-sessions.subagent_launched","data":{"cwd":"/old","childSessionFile":"/sessions/old/a.jsonl","goal":"keep /old","requestResponse":true,"nextSteps":{"path":"/old"},"writerSessionId":"writer"}}`)
	after, n, err := Transform(input, "/old", "/new", map[string]string{"/sessions/old/a.jsonl": "/sessions/new/a.jsonl"}, true)
	if err != nil || n != 2 {
		t.Fatalf("%v %d", err, n)
	}
	for _, literal := range []string{`"goal":"keep /old"`, `"requestResponse":true`, `"nextSteps":{"path":"/old"}`, `"writerSessionId":"writer"`} {
		if !bytes.Contains(after, []byte(literal)) {
			t.Fatalf("legacy field changed: %s", literal)
		}
	}
}
