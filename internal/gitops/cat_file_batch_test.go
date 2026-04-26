package gitops

import (
	"os"
	"path/filepath"
	"testing"
)

// helper: init a fresh repo with one blob + one tree + one commit.
func setupRepoWithBlob(t *testing.T) (*Git, string, string) {
	t.Helper()
	dir := t.TempDir()
	if err := Init(dir); err != nil {
		t.Fatalf("git init: %v", err)
	}
	g := NewFromRoot(dir)
	if _, err := g.Run("config", "user.email", "t@e"); err != nil {
		t.Fatalf("config email: %v", err)
	}
	if _, err := g.Run("config", "user.name", "t"); err != nil {
		t.Fatalf("config name: %v", err)
	}
	payload := []byte(`{"hello":"world"}`)
	blob, err := g.HashObject(payload)
	if err != nil {
		t.Fatalf("hash-object: %v", err)
	}
	tree, err := g.MakeTree("event.json", blob)
	if err != nil {
		t.Fatalf("mktree: %v", err)
	}
	commit, err := g.CommitTree(tree, "", "test")
	if err != nil {
		t.Fatalf("commit-tree: %v", err)
	}
	if err := g.UpdateRef("refs/test/c1", commit); err != nil {
		t.Fatalf("update-ref: %v", err)
	}
	// Sanity: blob is reachable on disk.
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Fatalf("expected .git: %v", err)
	}
	return g, blob, tree
}

func TestCatFileBatch_ReadBlob(t *testing.T) {
	g, blob, _ := setupRepoWithBlob(t)
	b, err := g.OpenCatFileBatch()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer b.Close()

	got, err := b.Read(blob)
	if err != nil {
		t.Fatalf("read by sha: %v", err)
	}
	if string(got) != `{"hello":"world"}` {
		t.Fatalf("body mismatch: %q", got)
	}
}

func TestCatFileBatch_ReadTreePath(t *testing.T) {
	g, _, tree := setupRepoWithBlob(t)
	b, err := g.OpenCatFileBatch()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer b.Close()

	got, err := b.Read(tree + ":event.json")
	if err != nil {
		t.Fatalf("read by tree-path: %v", err)
	}
	if string(got) != `{"hello":"world"}` {
		t.Fatalf("body mismatch: %q", got)
	}
}

func TestCatFileBatch_Missing(t *testing.T) {
	g, _, _ := setupRepoWithBlob(t)
	b, err := g.OpenCatFileBatch()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer b.Close()

	got, err := b.Read("0000000000000000000000000000000000000000")
	if err != nil {
		t.Fatalf("missing should not error: %v", err)
	}
	if got != nil {
		t.Fatalf("missing should return nil body, got %q", got)
	}
}

func TestCatFileBatch_MultipleSequential(t *testing.T) {
	g, blob, tree := setupRepoWithBlob(t)
	b, err := g.OpenCatFileBatch()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer b.Close()

	// Mix present + missing + present to verify the reader stays in
	// sync after each protocol form.
	for i := 0; i < 3; i++ {
		got, err := b.Read(blob)
		if err != nil || string(got) != `{"hello":"world"}` {
			t.Fatalf("read iter %d: %v / %q", i, err, got)
		}
		if got, err := b.Read("0000000000000000000000000000000000000000"); err != nil || got != nil {
			t.Fatalf("missing iter %d: %v / %q", i, err, got)
		}
		got, err = b.Read(tree + ":event.json")
		if err != nil || string(got) != `{"hello":"world"}` {
			t.Fatalf("path iter %d: %v / %q", i, err, got)
		}
	}
}

func TestCatFileBatch_CloseIdempotent(t *testing.T) {
	g, _, _ := setupRepoWithBlob(t)
	b, err := g.OpenCatFileBatch()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}
