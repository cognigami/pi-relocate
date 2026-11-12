package relocate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEffectiveGitWorktreeRefused(t *testing.T) {
	root, o, s, d, _ := basicFixture(t, false)
	put(t, filepath.Join(os.Getenv("HOME"), ".gitconfig"), []byte(fmt.Sprintf("[core]\nworktree = %s\n", s)))
	before, err := inventory(root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Discover(s, d, o)
	if err == nil || !strings.Contains(err.Error(), "core.worktree") {
		t.Fatalf("expected effective worktree refusal: %v", err)
	}
	if err := unchanged(root, before); err != nil {
		t.Fatal(err)
	}
}

func TestNestedVCRefused(t *testing.T) {
	for _, kind := range []string{"git-file", "git-link", "jj-file", "git-directory"} {
		t.Run(kind, func(t *testing.T) {
			root, o, s, d, _ := basicFixture(t, false)
			marker := filepath.Join(s, "nested", ".git")
			if kind == "jj-file" {
				marker = filepath.Join(s, "nested", ".jj")
			}
			if kind == "git-link" {
				if err := os.MkdirAll(filepath.Dir(marker), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(s, ".git"), marker); err != nil {
					t.Fatal(err)
				}
			} else if kind == "git-directory" {
				if err := os.MkdirAll(marker, 0700); err != nil {
					t.Fatal(err)
				}
			} else {
				put(t, marker, []byte("linked metadata"))
			}
			before, err := inventory(root)
			if err != nil {
				t.Fatal(err)
			}
			_, err = Discover(s, d, o)
			if err == nil || !strings.Contains(err.Error(), "nested VC") {
				t.Fatalf("expected nested VC refusal: %v", err)
			}
			if err := unchanged(root, before); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestResumeCommandShellQuotedPathsRefused(t *testing.T) {
	source := "/work/parent's"
	input := []byte(`{"type":"custom","customType":"pi-sessions.subagent_launched","data":{"cwd":"/work/parent's","childSessionFile":"/sessions/old/a.jsonl","resumeCommand":"cd '/work/parent'\"'\"'s' && pi --session-id child"}}`)
	if err := unsupportedReference(input, source, map[string]string{"/sessions/old/a.jsonl": "/sessions/new/a.jsonl"}); err == nil {
		t.Fatal("shell-quoted moving path was not refused")
	}
	// Transform remains narrow even for a command that the planner refuses.
	after, _, err := Transform(input, source, "/work/new", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	var record struct {
		Data struct {
			Resume string `json:"resumeCommand"`
		} `json:"data"`
	}
	if err := json.Unmarshal(after, &record); err != nil {
		t.Fatal(err)
	}
	if record.Data.Resume != `cd '/work/parent'"'"'s' && pi --session-id child` {
		t.Fatal("command value was rewritten")
	}
}
