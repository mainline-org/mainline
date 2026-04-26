package gitops

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mainline-org/mainline/internal/domain"
)

// Git wraps git CLI operations for a repository.
type Git struct {
	RepoRoot string
}

// New creates a Git wrapper. It discovers the repo root from the given dir.
func New(dir string) (*Git, error) {
	root, err := run(dir, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, &domain.MainlineError{
			Code:    domain.ErrNotInGitRepo,
			Message: "not inside a git repository",
		}
	}
	return &Git{RepoRoot: strings.TrimSpace(root)}, nil
}

// NewFromRoot creates a Git wrapper for a known repo root.
func NewFromRoot(root string) *Git {
	return &Git{RepoRoot: root}
}

func (g *Git) run(args ...string) (string, error) {
	return run(g.RepoRoot, "git", args...)
}

// Run executes an arbitrary git command and returns stdout.
func (g *Git) Run(args ...string) (string, error) {
	return g.run(args...)
}

func run(dir string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %s (stderr: %s)", strings.Join(args, " "), err, stderr.String())
	}
	return stdout.String(), nil
}

// CurrentBranch returns the current branch name.
func (g *Git) CurrentBranch() (string, error) {
	out, err := g.run("rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// HeadCommit returns the HEAD commit hash.
func (g *Git) HeadCommit() (string, error) {
	out, err := g.run("rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// BranchExists checks if a local branch exists.
func (g *Git) BranchExists(name string) bool {
	_, err := g.run("rev-parse", "--verify", "refs/heads/"+name)
	return err == nil
}

// CreateBranch creates a new branch at the given start point.
func (g *Git) CreateBranch(name, startPoint string) error {
	_, err := g.run("branch", name, startPoint)
	return err
}

// CheckoutBranch checks out a branch.
func (g *Git) CheckoutBranch(name string) error {
	_, err := g.run("checkout", name)
	return err
}

// MainBranch returns the name of the main branch (main or master).
func (g *Git) MainBranch() string {
	if g.BranchExists("main") {
		return "main"
	}
	if g.BranchExists("master") {
		return "master"
	}
	return "main"
}

// MergeBase returns the merge-base commit between two refs.
func (g *Git) MergeBase(a, b string) (string, error) {
	out, err := g.run("merge-base", a, b)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// DiffStatAgainst returns diff stats between two refs.
func (g *Git) DiffStatAgainst(base, head string) (domain.DiffStats, []domain.FileChange, error) {
	out, err := g.run("diff", "--numstat", base, head)
	if err != nil {
		return domain.DiffStats{}, nil, err
	}

	var stats domain.DiffStats
	var changes []domain.FileChange
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 3 {
			continue
		}
		added, _ := strconv.Atoi(parts[0])
		removed, _ := strconv.Atoi(parts[1])
		file := parts[2]
		stats.Files++
		stats.Added += added
		stats.Removed += removed
		changes = append(changes, domain.FileChange{
			Path:    file,
			Status:  "modified",
			Added:   added,
			Removed: removed,
		})
	}

	// Enrich with --name-status for accurate status
	statusOut, err := g.run("diff", "--name-status", base, head)
	if err == nil {
		statusMap := make(map[string]string)
		for _, line := range strings.Split(strings.TrimSpace(statusOut), "\n") {
			if line == "" {
				continue
			}
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				status := parts[0]
				file := parts[len(parts)-1]
				switch {
				case strings.HasPrefix(status, "A"):
					statusMap[file] = "added"
				case strings.HasPrefix(status, "D"):
					statusMap[file] = "deleted"
				case strings.HasPrefix(status, "R"):
					statusMap[file] = "renamed"
				case strings.HasPrefix(status, "C"):
					statusMap[file] = "copied"
				default:
					statusMap[file] = "modified"
				}
			}
		}
		for i, fc := range changes {
			if s, ok := statusMap[fc.Path]; ok {
				changes[i].Status = s
			}
		}
	}

	return stats, changes, nil
}

// DiffFilesAgainst returns the list of changed file paths.
func (g *Git) DiffFilesAgainst(base, head string) ([]string, error) {
	out, err := g.run("diff", "--name-only", base, head)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, f := range strings.Split(strings.TrimSpace(out), "\n") {
		if f != "" {
			files = append(files, f)
		}
	}
	return files, nil
}

// HashObject writes a blob to the object database and returns its hash.
func (g *Git) HashObject(data []byte) (string, error) {
	cmd := exec.Command("git", "hash-object", "-w", "--stdin")
	cmd.Dir = g.RepoRoot
	cmd.Stdin = bytes.NewReader(data)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("hash-object: %s", stderr.String())
	}
	return strings.TrimSpace(stdout.String()), nil
}

// MakeTree creates a tree object with one blob entry.
func (g *Git) MakeTree(filename, blobHash string) (string, error) {
	entry := fmt.Sprintf("100644 blob %s\t%s\n", blobHash, filename)
	cmd := exec.Command("git", "mktree")
	cmd.Dir = g.RepoRoot
	cmd.Stdin = strings.NewReader(entry)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("mktree: %s", stderr.String())
	}
	return strings.TrimSpace(stdout.String()), nil
}

// MakeTreeMulti creates a tree object with multiple blob entries.
func (g *Git) MakeTreeMulti(entries []TreeEntry) (string, error) {
	var input strings.Builder
	for _, e := range entries {
		fmt.Fprintf(&input, "100644 blob %s\t%s\n", e.Hash, e.Name)
	}
	cmd := exec.Command("git", "mktree")
	cmd.Dir = g.RepoRoot
	cmd.Stdin = strings.NewReader(input.String())
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("mktree: %s", stderr.String())
	}
	return strings.TrimSpace(stdout.String()), nil
}

type TreeEntry struct {
	Name string
	Hash string
}

// CommitTree creates a commit object.
func (g *Git) CommitTree(tree, parent, message string) (string, error) {
	args := []string{"commit-tree", tree, "-m", message}
	if parent != "" {
		args = []string{"commit-tree", tree, "-p", parent, "-m", message}
	}
	out, err := g.run(args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// UpdateRef updates a ref to point to a commit.
func (g *Git) UpdateRef(ref, commit string) error {
	_, err := g.run("update-ref", ref, commit)
	return err
}

// ReadRef resolves a ref to a commit hash. Returns empty string if not found.
func (g *Git) ReadRef(ref string) string {
	out, err := g.run("rev-parse", "--verify", ref)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// ListRefs returns fully qualified ref names under the given prefix.
func (g *Git) ListRefs(prefix string) ([]string, error) {
	out, err := g.run("for-each-ref", "--format=%(refname)", prefix)
	if err != nil {
		return nil, err
	}
	var refs []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		ref := strings.TrimSpace(line)
		if ref != "" {
			refs = append(refs, ref)
		}
	}
	return refs, nil
}

// CatBlob returns the content of a blob object.
func (g *Git) CatBlob(hash string) ([]byte, error) {
	out, err := g.run("cat-file", "-p", hash)
	if err != nil {
		return nil, err
	}
	return []byte(out), nil
}

// ListTree returns entries in a tree object.
func (g *Git) ListTree(treeHash string) ([]TreeEntry, error) {
	out, err := g.run("ls-tree", treeHash)
	if err != nil {
		return nil, err
	}
	var entries []TreeEntry
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		// format: <mode> <type> <hash>\t<name>
		tabIdx := strings.IndexByte(line, '\t')
		if tabIdx < 0 {
			continue
		}
		meta := strings.Fields(line[:tabIdx])
		name := line[tabIdx+1:]
		if len(meta) >= 3 {
			entries = append(entries, TreeEntry{Name: name, Hash: meta[2]})
		}
	}
	return entries, nil
}

// Fetch fetches from a remote.
func (g *Git) Fetch(remote string, refspecs ...string) error {
	args := append([]string{"fetch", remote}, refspecs...)
	_, err := g.run(args...)
	return err
}

// Push pushes refs to a remote.
func (g *Git) Push(remote string, refspecs ...string) error {
	args := append([]string{"push", remote}, refspecs...)
	_, err := g.run(args...)
	return err
}

// LogOneline returns recent commit summaries.
func (g *Git) LogOneline(ref string, n int) ([]LogEntry, error) {
	out, err := g.run("log", "--format=%H|%s|%aI", "-n", strconv.Itoa(n), ref)
	if err != nil {
		return nil, err
	}
	var entries []LogEntry
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 3)
		if len(parts) == 3 {
			entries = append(entries, LogEntry{
				Hash:    parts[0],
				Subject: parts[1],
				Date:    parts[2],
			})
		}
	}
	return entries, nil
}

type LogEntry struct {
	Hash    string
	Subject string
	Date    string
}

// CommitTrailers returns the trailers from a commit message.
func (g *Git) CommitTrailers(commitHash string) (map[string]string, error) {
	out, err := g.run("log", "-1", "--format=%(trailers:key,valueonly)", commitHash)
	if err != nil {
		return nil, err
	}
	return ParseTrailers(out), nil
}

// ParseTrailers parses git trailer lines into a map.
func ParseTrailers(raw string) map[string]string {
	result := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		idx := strings.Index(line, ":")
		if idx > 0 {
			key := strings.TrimSpace(line[:idx])
			value := strings.TrimSpace(line[idx+1:])
			result[key] = value
		}
	}
	return result
}

// FullCommitMessage returns the full commit message.
func (g *Git) FullCommitMessage(commitHash string) (string, error) {
	out, err := g.run("log", "-1", "--format=%B", commitHash)
	if err != nil {
		return "", err
	}
	return out, nil
}

// CommitDate returns a commit's author date as an ISO 8601 string.
func (g *Git) CommitDate(commitHash string) (string, error) {
	out, err := g.run("log", "-1", "--format=%aI", commitHash)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// CommitTreeHash returns the tree hash of a commit. Two commits with the
// same tree hash have byte-identical working trees — the property that lets
// pin (formerly reconcile) recognise a squash merge as the merged form of
// a feature branch.
func (g *Git) CommitTreeHash(commitHash string) (string, error) {
	out, err := g.run("log", "-1", "--format=%T", commitHash)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// HasRemote checks if a remote exists.
func (g *Git) HasRemote(name string) bool {
	out, _ := g.run("remote")
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == name {
			return true
		}
	}
	return false
}

// Init initializes a git repo at the given path (for testing).
func Init(dir string) error {
	_, err := run(dir, "git", "init")
	return err
}

// MainlineDir returns the .mainline directory path.
func (g *Git) MainlineDir() string {
	return filepath.Join(g.RepoRoot, ".mainline")
}

// CacheDir returns the .ml-cache directory path.
func (g *Git) CacheDir() string {
	return filepath.Join(g.RepoRoot, ".ml-cache")
}

// IsTreeClean returns true if working tree has no uncommitted changes.
func (g *Git) IsTreeClean() bool {
	out, err := g.run("status", "--porcelain")
	if err != nil {
		return false
	}
	return strings.TrimSpace(out) == ""
}

// WriteAndCommitFile stages and commits a single file (used for .mainline config).
func (g *Git) WriteAndCommitFile(relPath, content, message string) error {
	fullPath := filepath.Join(g.RepoRoot, relPath)
	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
		return err
	}
	if _, err := g.run("add", relPath); err != nil {
		return err
	}
	_, err := g.run("commit", "-m", message)
	return err
}

// EnsureGitignore adds patterns to .gitignore if not present.
func (g *Git) EnsureGitignore(patterns []string) error {
	gitignorePath := filepath.Join(g.RepoRoot, ".gitignore")
	existing := ""
	if data, err := os.ReadFile(gitignorePath); err == nil {
		existing = string(data)
	}
	var toAdd []string
	for _, p := range patterns {
		if !strings.Contains(existing, p) {
			toAdd = append(toAdd, p)
		}
	}
	if len(toAdd) == 0 {
		return nil
	}
	f, err := os.OpenFile(gitignorePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if existing != "" && !strings.HasSuffix(existing, "\n") {
		f.WriteString("\n")
	}
	for _, p := range toAdd {
		f.WriteString(p + "\n")
	}
	return nil
}

// -----------------------------------------------------------
// Git Notes operations (refs/notes/mainline/intents)
// -----------------------------------------------------------

const NotesRef = "refs/notes/mainline/intents"

// NotesAdd attaches a note to a commit under the mainline notes ref.
func (g *Git) NotesAdd(commitHash, content string) error {
	_, err := g.run("notes", "--ref=mainline/intents", "add", "-f", "-m", content, commitHash)
	return err
}

// NotesShow returns the note content for a commit, or empty string if none.
func (g *Git) NotesShow(commitHash string) (string, error) {
	out, err := g.run("notes", "--ref=mainline/intents", "show", commitHash)
	if err != nil {
		return "", nil // no note is not an error
	}
	return strings.TrimSpace(out), nil
}

// NotesListCommits returns all commit hashes that have notes in the mainline ref.
func (g *Git) NotesListCommits() ([]string, error) {
	out, err := g.run("notes", "--ref=mainline/intents", "list")
	if err != nil {
		return nil, nil // no notes ref yet
	}
	var commits []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		// format: <note-hash> <commit-hash>
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			commits = append(commits, parts[1])
		}
	}
	return commits, nil
}

// ConfigAdd runs git config --add for a key=value pair.
func (g *Git) ConfigAdd(key, value string) error {
	_, err := g.run("config", "--add", key, value)
	return err
}

// ConfigGet returns the value of a git config key, empty if not set.
func (g *Git) ConfigGet(key string) string {
	out, err := g.run("config", "--get-all", key)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}
