package gitops

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDiffStatAgainstHandlesUTF8AndSpaces(t *testing.T) {
	repo := initGitRepo(t)
	changed := "dir with space/中文 file.md"
	added := "new 中文.md"

	writeRepoFile(t, repo, changed, "one\n")
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-m", "init")

	writeRepoFile(t, repo, changed, "one\ntwo\n")
	writeRepoFile(t, repo, added, "hello\n")
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-m", "change")

	g := NewFromRoot(repo)
	stats, changes, err := g.DiffStatAgainst("HEAD~1", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if stats.Files != 2 {
		t.Fatalf("expected two changed files, got stats=%+v changes=%+v", stats, changes)
	}

	got := map[string]string{}
	for _, c := range changes {
		if strings.Contains(c.Path, `\345`) || strings.Contains(c.Path, `"`) {
			t.Fatalf("path should be raw UTF-8, not git-quoted: %q", c.Path)
		}
		got[c.Path] = c.Status
	}
	want := map[string]string{changed: "modified", added: "added"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("changes mismatch\ngot:  %#v\nwant: %#v", got, want)
	}

	files, err := g.DiffFilesAgainst("HEAD~1", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(files, []string{changed, added}) {
		t.Fatalf("diff files mismatch\ngot:  %#v\nwant: %#v", files, []string{changed, added})
	}
}

func TestDiffStatAgainstUsesRenameDestinationPath(t *testing.T) {
	repo := initGitRepo(t)
	oldPath := "old 中文.md"
	newPath := "new 中文 file.md"

	writeRepoFile(t, repo, oldPath, "one\n")
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-m", "init")
	git(t, repo, "mv", oldPath, newPath)
	git(t, repo, "commit", "-m", "rename")

	g := NewFromRoot(repo)
	_, changes, err := g.DiffStatAgainst("HEAD~1", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 {
		t.Fatalf("expected one rename change, got %+v", changes)
	}
	if changes[0].Path != newPath || changes[0].Status != "renamed" {
		t.Fatalf("rename should point at destination path/status, got %+v", changes[0])
	}
}

func initGitRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	git(t, repo, "init")
	git(t, repo, "config", "user.email", "test@example.com")
	git(t, repo, "config", "user.name", "Test User")
	return repo
}

func writeRepoFile(t *testing.T, repo, rel, body string) {
	t.Helper()
	path := filepath.Join(repo, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func git(t *testing.T, repo string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = repo
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}
