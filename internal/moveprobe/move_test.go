// These integration tests establish the supported VC move mechanism before the
// relocation mutator is implemented. Only repository-local disposable fixtures
// are used. Commands initialize fixtures; readback explicitly disables snapshots.
package moveprobe

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestColocatedRename(t *testing.T) {
	for _, container := range []bool{false, true} {
		t.Run(fmt.Sprint("container=", container), func(t *testing.T) {
			base, err := filepath.Abs("../../.fixtures")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(base, 0700); err != nil {
				t.Fatal(err)
			}
			dir, err := os.MkdirTemp(base, "vc-proof-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := os.RemoveAll(dir); err != nil {
					t.Error(err)
				}
			})
			home := filepath.Join(dir, "home")
			if err := os.Mkdir(home, 0700); err != nil {
				t.Fatal(err)
			}
			env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "XDG_CONFIG_HOME=" + home, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + filepath.Join(home, "gitconfig"), "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid", "JJ_CONFIG=" + filepath.Join(home, "jj.toml")}
			if err := os.WriteFile(filepath.Join(home, "jj.toml"), []byte("[user]\nname = 'Fixture'\nemail = 'fixture@example.invalid'\n"), 0600); err != nil {
				t.Fatal(err)
			}
			cmd := func(at, name string, args ...string) string {
				t.Helper()
				c := exec.Command(name, args...)
				c.Dir = at
				c.Env = env
				out, err := c.CombinedOutput()
				if err != nil {
					t.Fatalf("%s %v: %v\n%s", name, args, err, out)
				}
				return string(out)
			}
			t.Log(strings.TrimSpace(cmd(dir, "jj", "--version")), strings.TrimSpace(cmd(dir, "git", "--version")))
			origin := filepath.Join(dir, "origin")
			cmd(dir, "git", "init", origin)
			write := func(path, content string) {
				t.Helper()
				if err := os.WriteFile(path, []byte(content), 0644); err != nil {
					t.Fatal(err)
				}
			}
			write(filepath.Join(origin, "tracked"), "committed\n")
			write(filepath.Join(origin, ".gitignore"), "ignored\n")
			cmd(origin, "git", "add", ".")
			cmd(origin, "git", "commit", "-m", "fixture")
			source := filepath.Join(dir, "perry")
			cmd(dir, "git", "clone", origin, source)
			cmd(source, "jj", "git", "init", "--colocate")
			cmd(source, "jj", "bookmark", "create", "fixture-bookmark")
			write(filepath.Join(source, "tracked"), "dirty\n")
			write(filepath.Join(source, "untracked"), "new\n")
			write(filepath.Join(source, "ignored"), "ignored bytes\n")
			target, err := os.ReadFile(filepath.Join(source, ".jj", "repo", "store", "git_target"))
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("git_target=%q", target)
			if filepath.IsAbs(strings.TrimSpace(string(target))) {
				t.Fatal("JJ Git target must be relative")
			}
			snapshot := func(root string) map[string]string {
				t.Helper()
				m := map[string]string{}
				err := filepath.WalkDir(root, func(path string, d fs.DirEntry, e error) error {
					if e != nil {
						return e
					}
					if d.IsDir() {
						return nil
					}
					data, e := os.ReadFile(path)
					if e != nil {
						return e
					}
					i, e := d.Info()
					if e != nil {
						return e
					}
					rel, e := filepath.Rel(root, path)
					if e != nil {
						return e
					}
					m[rel] = fmt.Sprintf("%x %v %v", sha256.Sum256(data), i.Mode(), i.ModTime().UnixNano())
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				return m
			}
			reads := func(root string) []string {
				return []string{cmd(root, "git", "rev-parse", "HEAD"), cmd(root, "git", "show-ref"), cmd(root, "jj", "--ignore-working-copy", "--no-pager", "log", "--no-graph", "-T", "commit_id ++ change_id ++ description"), cmd(root, "jj", "--ignore-working-copy", "--no-pager", "bookmark", "list"), cmd(root, "jj", "--ignore-working-copy", "--no-pager", "workspace", "list"), cmd(root, "jj", "--ignore-working-copy", "--no-pager", "root")}
			}
			before := snapshot(source)
			refsBefore := reads(source)
			if !reflect.DeepEqual(before, snapshot(source)) {
				t.Fatal("readback mutated fixture")
			}
			destination := filepath.Join(dir, "relocated")
			if container {
				destination = filepath.Join(source, "root")
				stage := filepath.Join(dir, "stage")
				if err := os.Rename(source, stage); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(source, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(stage, destination); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Rename(source, destination); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, snapshot(destination)) {
				t.Fatal("rename changed checkout bytes, modes or mtimes")
			}
			refsAfter := reads(destination)
			if !reflect.DeepEqual(refsBefore[:len(refsBefore)-1], refsAfter[:len(refsAfter)-1]) {
				t.Fatalf("VC references changed\nbefore=%q\nafter=%q", refsBefore, refsAfter)
			}
			if strings.TrimSpace(refsAfter[len(refsAfter)-1]) != destination {
				t.Fatal("JJ root did not follow rename")
			}
			if !reflect.DeepEqual(before, snapshot(destination)) {
				t.Fatal("post-move readback changed raw VC/workcopy state")
			}
			t.Log("All checkout files, raw .git/.jj metadata, bookmarks, operation/workcopy state, dirty/untracked/ignored bytes and mtimes preserved; no location repair required.")
		})
	}
}
