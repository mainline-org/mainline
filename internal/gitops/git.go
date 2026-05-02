package gitops

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

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
			SuggestedActions: []string{
				"`git init` here, or cd to an existing repo",
				"then re-run `mainline init --actor-name \"<your name>\"`",
			},
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

// CommonDir returns git's common directory for this checkout. In a
// linked worktree this is the original repository's .git directory;
// in a normal checkout it is usually ".git". Mainline uses it for
// clone-local state that must be shared across worktrees without being
// committed.
func (g *Git) CommonDir() (string, error) {
	out, err := g.run("rev-parse", "--git-common-dir")
	if err != nil {
		return "", err
	}
	path := strings.TrimSpace(out)
	if path == "" {
		return "", fmt.Errorf("empty git common dir")
	}
	if filepath.IsAbs(path) {
		return path, nil
	}
	return filepath.Clean(filepath.Join(g.RepoRoot, path)), nil
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

// UpdateRefIfEquals updates a ref only if it still points at oldCommit.
// An empty oldCommit means the ref must not exist. Callers use this as
// a compare-and-swap primitive when appending to git-backed logs.
func (g *Git) UpdateRefIfEquals(ref, commit, oldCommit string) error {
	if oldCommit == "" {
		oldCommit = "0000000000000000000000000000000000000000"
	}
	_, err := g.run("update-ref", ref, commit, oldCommit)
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

// LogWindowEntry is the per-commit summary returned by LogWindow. Adds
// Author on top of LogEntry so coverage / status surfaces can show
// "who made this commit" without a second log invocation.
type LogWindowEntry struct {
	Hash    string
	Subject string
	Author  string
	Date    string
}

// LogWindow returns the last n commits on ref, newest first, with
// hash + subject + author + ISO date in one git invocation. Used by
// the coverage scanner.
func (g *Git) LogWindow(ref string, n int) ([]LogWindowEntry, error) {
	if n <= 0 {
		n = 30
	}
	out, err := g.run("log", "--format=%H%x09%s%x09%an%x09%aI", "-n", strconv.Itoa(n), ref)
	if err != nil {
		return nil, err
	}
	var entries []LogWindowEntry
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 4)
		if len(parts) == 4 {
			entries = append(entries, LogWindowEntry{
				Hash:    parts[0],
				Subject: parts[1],
				Author:  parts[2],
				Date:    parts[3],
			})
		}
	}
	return entries, nil
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

// CommitSubject returns the first line of a commit's message (git's %s
// format specifier). The subject is what survives a clean rebase even
// when the tree and parent change — `git commit --amend` without
// editing the message keeps the same subject, and `git rebase --onto`
// preserves subjects unless the user explicitly reworded them. That
// makes the subject the single most reliable signal for "is this main
// commit the rebased form of my code_commit?" when tree_hash and
// commit_hash both fail.
func (g *Git) CommitSubject(commitHash string) (string, error) {
	out, err := g.run("log", "-1", "--format=%s", commitHash)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
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

// WorktreeStatusReport classifies the worktree as clean, dirty, or
// untracked, with the offending file paths in DirtyFiles. Used by
// seal --prepare/--submit to enforce the v0.3 snapshot contract.
type WorktreeStatusReport struct {
	Status     string   // "clean" | "dirty" | "untracked"
	DirtyFiles []string // tracked-but-modified paths (when Status == "dirty")
	Untracked  []string // untracked paths (when Status == "untracked")
}

// WorktreeStatus reports whether the worktree is clean, has tracked
// modifications, or has untracked files. "Untracked" wins over "dirty"
// when both are present — the more surprising case to a reader, so we
// surface it.
func (g *Git) WorktreeStatus() (*WorktreeStatusReport, error) {
	out, err := g.run("status", "--porcelain")
	if err != nil {
		return nil, err
	}
	rep := &WorktreeStatusReport{Status: "clean"}
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 3 {
			continue
		}
		// porcelain v1: "XY <path>" where XY is two status chars.
		code := line[:2]
		path := strings.TrimSpace(line[3:])
		if path == "" {
			continue
		}
		if code == "??" {
			rep.Untracked = append(rep.Untracked, path)
		} else {
			rep.DirtyFiles = append(rep.DirtyFiles, path)
		}
	}
	switch {
	case len(rep.Untracked) > 0:
		rep.Status = "untracked"
	case len(rep.DirtyFiles) > 0:
		rep.Status = "dirty"
	}
	return rep, nil
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
		if _, err := f.WriteString("\n"); err != nil {
			return fmt.Errorf("gitignore separator newline: %w", err)
		}
	}
	for _, p := range toAdd {
		if _, err := f.WriteString(p + "\n"); err != nil {
			return fmt.Errorf("gitignore append %q: %w", p, err)
		}
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

// -----------------------------------------------------------
// cat-file --batch: long-lived subprocess for piped object reads
// -----------------------------------------------------------
//
// Why this exists. Walking actor logs and fetching note bodies would
// otherwise spawn one git process per object; at ~20ms fork cost, that
// dominated sync wall time once the per-event ops were already
// parallelised across actors. One long-lived process answering N piped
// queries is the canonical git-CLI pattern (used by git-lfs,
// git-filter-repo, git-machete) and cuts per-object cost to ~50µs.

// CatFileBatch wraps a `git cat-file --batch` subprocess. Each Read
// sends one object spec on stdin and consumes one response from
// stdout; concurrent Read calls serialise through the internal mutex.
type CatFileBatch struct {
	mu     sync.Mutex
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	closed bool
}

// OpenCatFileBatch starts a long-lived `git cat-file --batch`
// subprocess. Caller MUST call Close.
func (g *Git) OpenCatFileBatch() (*CatFileBatch, error) {
	cmd := exec.Command("git", "cat-file", "--batch")
	cmd.Dir = g.RepoRoot
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("cat-file batch stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("cat-file batch stdout: %w", err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("cat-file batch start: %w", err)
	}
	return &CatFileBatch{
		cmd:    cmd,
		stdin:  stdin,
		stdout: bufio.NewReader(stdout),
	}, nil
}

// Read returns the byte content of the object identified by spec
// (a sha, ref, or tree-ish:path). Returns nil for "missing" responses
// — the documented `<spec> missing` protocol reply — so callers can
// treat absence as a non-error.
func (b *CatFileBatch) Read(spec string) ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, fmt.Errorf("cat-file batch: closed")
	}

	if _, err := fmt.Fprintf(b.stdin, "%s\n", spec); err != nil {
		return nil, fmt.Errorf("cat-file batch write: %w", err)
	}

	header, err := b.stdout.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("cat-file batch header: %w", err)
	}
	header = strings.TrimRight(header, "\n")

	// Missing-object form: "<spec> missing" with no body following.
	if strings.HasSuffix(header, " missing") {
		return nil, nil
	}

	// Present-object form: "<sha> <type> <size>\n<size bytes>\n"
	parts := strings.Fields(header)
	if len(parts) != 3 {
		return nil, fmt.Errorf("cat-file batch: unexpected header %q", header)
	}
	size, err := strconv.Atoi(parts[2])
	if err != nil {
		return nil, fmt.Errorf("cat-file batch: bad size in %q", header)
	}

	body := make([]byte, size)
	if _, err := io.ReadFull(b.stdout, body); err != nil {
		return nil, fmt.Errorf("cat-file batch body: %w", err)
	}
	// Protocol terminates each object with a trailing newline.
	if _, err := b.stdout.ReadByte(); err != nil {
		return nil, fmt.Errorf("cat-file batch trailing: %w", err)
	}
	return body, nil
}

// Close shuts the subprocess down cleanly. Safe to call multiple times.
func (b *CatFileBatch) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	if b.stdin != nil {
		b.stdin.Close()
	}
	if b.cmd != nil {
		return b.cmd.Wait()
	}
	return nil
}

// -----------------------------------------------------------
// Bulk helpers that fold N forks into 1
// -----------------------------------------------------------

// CommitTree pairs a commit hash with its tree hash.
type CommitTree struct {
	Commit string
	Tree   string
}

// LogChainTrees walks ref's first-parent chain (newest first) and
// returns every (commit, tree) pair. One fork replaces the per-commit
// `log %T` + `log %P` pair callers used to do while walking themselves.
func (g *Git) LogChainTrees(ref string) ([]CommitTree, error) {
	out, err := g.run("log", "--first-parent", "--format=%H %T", ref)
	if err != nil {
		return nil, err
	}
	var entries []CommitTree
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) == 2 {
			entries = append(entries, CommitTree{Commit: parts[0], Tree: parts[1]})
		}
	}
	return entries, nil
}

// RevListSet returns every commit reachable from ref as a set. One
// fork replaces N `merge-base --is-ancestor` calls when the caller has
// many commits to test against the same ref.
func (g *Git) RevListSet(ref string) (map[string]bool, error) {
	commits, err := g.RevList(ref)
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool)
	for _, commit := range commits {
		set[commit] = true
	}
	return set, nil
}

// RevList returns every commit reachable from ref, newest first.
func (g *Git) RevList(ref string) ([]string, error) {
	out, err := g.run("rev-list", ref)
	if err != nil {
		return nil, err
	}
	var commits []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			commits = append(commits, line)
		}
	}
	return commits, nil
}

// NoteEntry pairs a note blob hash with the commit it annotates.
type NoteEntry struct {
	NoteBlob   string
	CommitHash string
}

// NotesListEntries returns every entry on the mainline notes ref as a
// (note-blob, target-commit) pair. Callers that only need the note
// content should fetch by note-blob via cat-file --batch — much
// cheaper than running `git notes show` per commit, which re-resolves
// the path each time.
func (g *Git) NotesListEntries() ([]NoteEntry, error) {
	out, err := g.run("notes", "--ref=mainline/intents", "list")
	if err != nil {
		return nil, nil // no notes ref yet — same semantics as NotesListCommits
	}
	var entries []NoteEntry
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			entries = append(entries, NoteEntry{
				NoteBlob:   parts[0],
				CommitHash: parts[1],
			})
		}
	}
	return entries, nil
}

// CommitParents returns parent hashes keyed by commit hash. One
// `log --no-walk` invocation regardless of N. Merge commits have 2+
// parents; the second parent is the branch tip that was merged.
func (g *Git) CommitParents(commits []string) (map[string][]string, error) {
	if len(commits) == 0 {
		return nil, nil
	}
	// %H = commit, %P = space-separated parent hashes.
	// Use tab to separate commit from parents so we can split reliably.
	args := append([]string{"log", "--no-walk", "--format=%H\t%P"}, commits...)
	out, err := g.run(args...)
	if err != nil {
		return nil, err
	}
	parents := make(map[string][]string)
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) < 1 {
			continue
		}
		hash := parts[0]
		if len(parts) == 2 && parts[1] != "" {
			parents[hash] = strings.Fields(parts[1])
		} else {
			parents[hash] = nil // root commit
		}
	}
	return parents, nil
}

// CommitTreeHashes returns tree hashes keyed by commit hash. One
// `log --no-walk` invocation regardless of N; replaces per-commit
// CommitTreeHash when callers know the full set up-front (e.g. the
// auto-pin sweep that needs the tree of every recent main commit).
func (g *Git) CommitTreeHashes(commits []string) (map[string]string, error) {
	if len(commits) == 0 {
		return nil, nil
	}
	args := append([]string{"log", "--no-walk", "--format=%H %T"}, commits...)
	out, err := g.run(args...)
	if err != nil {
		return nil, err
	}
	trees := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) == 2 {
			trees[parts[0]] = parts[1]
		}
	}
	return trees, nil
}

// CommitDates returns ISO 8601 author dates keyed by commit hash. One
// `log --no-walk` invocation regardless of N; replaces per-commit
// CommitDate when callers know the full set up-front.
func (g *Git) CommitDates(commits []string) (map[string]string, error) {
	if len(commits) == 0 {
		return nil, nil
	}
	args := append([]string{"log", "--no-walk", "--format=%H %aI"}, commits...)
	out, err := g.run(args...)
	if err != nil {
		return nil, err
	}
	dates := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) == 2 {
			dates[parts[0]] = parts[1]
		}
	}
	return dates, nil
}

// CommitSubjects returns commit subjects (first line of message) keyed by
// commit hash. One `log --no-walk` invocation regardless of N; replaces
// per-commit CommitSubject when callers know the full set up-front.
func (g *Git) CommitSubjects(commits []string) (map[string]string, error) {
	if len(commits) == 0 {
		return nil, nil
	}
	// Use tab separator to handle subjects that may contain spaces.
	args := append([]string{"log", "--no-walk", "--format=%H\t%s"}, commits...)
	out, err := g.run(args...)
	if err != nil {
		return nil, err
	}
	subjects := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) == 2 {
			subjects[parts[0]] = parts[1]
		}
	}
	return subjects, nil
}

// FullCommitMessages returns full commit messages keyed by commit hash.
// Uses a single `git cat-file --batch` session to avoid N subprocess forks.
func (g *Git) FullCommitMessages(commits []string) (map[string]string, error) {
	if len(commits) == 0 {
		return nil, nil
	}
	batch, err := g.OpenCatFileBatch()
	if err != nil {
		// Fallback: run individually.
		msgs := make(map[string]string)
		for _, c := range commits {
			msg, _ := g.FullCommitMessage(c)
			msgs[c] = msg
		}
		return msgs, nil
	}
	defer batch.Close()

	msgs := make(map[string]string)
	for _, c := range commits {
		body, err := batch.Read(c)
		if err != nil || body == nil {
			continue
		}
		// cat-file on a commit returns the raw commit object.
		// The message starts after the first blank line.
		s := string(body)
		if idx := strings.Index(s, "\n\n"); idx >= 0 {
			msgs[c] = strings.TrimSpace(s[idx+2:])
		}
	}
	return msgs, nil
}

// NotesForCommits returns note contents keyed by commit hash for commits
// that have notes in the mainline/intents ref. Uses `git notes list` to
// get note blob OIDs, then reads them via `cat-file --batch` in one session.
func (g *Git) NotesForCommits(commits []string) (map[string]string, error) {
	if len(commits) == 0 {
		return nil, nil
	}
	// First, list all notes to get note_blob → commit mapping.
	out, err := g.run("notes", "--ref=mainline/intents", "list")
	if err != nil {
		return nil, nil // no notes ref
	}

	// Build commit→noteBlob map, filtered to requested commits.
	wantSet := make(map[string]struct{}, len(commits))
	for _, c := range commits {
		wantSet[c] = struct{}{}
	}
	type noteEntry struct {
		blob   string
		commit string
	}
	var toRead []noteEntry
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) != 2 {
			continue
		}
		blob, commit := parts[0], parts[1]
		if _, ok := wantSet[commit]; ok {
			toRead = append(toRead, noteEntry{blob: blob, commit: commit})
		}
	}

	if len(toRead) == 0 {
		return map[string]string{}, nil
	}

	// Read note blobs via cat-file --batch.
	batch, err := g.OpenCatFileBatch()
	if err != nil {
		// Fallback: read individually.
		notes := make(map[string]string)
		for _, c := range commits {
			n, _ := g.NotesShow(c)
			if n != "" {
				notes[c] = n
			}
		}
		return notes, nil
	}
	defer batch.Close()

	notes := make(map[string]string)
	for _, entry := range toRead {
		body, err := batch.Read(entry.blob)
		if err != nil || body == nil {
			continue
		}
		notes[entry.commit] = strings.TrimSpace(string(body))
	}
	return notes, nil
}

// ConfigGet returns the value of a git config key, empty if not set.
func (g *Git) ConfigGet(key string) string {
	out, err := g.run("config", "--get-all", key)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}
