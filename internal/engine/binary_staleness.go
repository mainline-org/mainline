package engine

import (
	"fmt"
	"os"
	"time"
)

// BinaryStalenessReport tells the agent (via session_start additional_context)
// whether the running mainline binary may be older than the latest main code.
//
// Motivation: hooks are wired in `~/.cursor/hooks.json` (or equivalent) by
// absolute path. After `git pull` the user must rebuild the binary, but
// hooks fire silently — there is no obvious cue when the running hook
// process is on stale code. Symptom: a freshly-fixed bug appears to
// silently regress in the next session because the hook is still on the
// pre-fix binary. The canonical example that motivated this check is the
// auto-Start-on-prompt regression that minted the user's *question text*
// as an intent goal even after PR #38 had merged the fix to drop that
// auto-flow — the hook wrapper was running the old binary.
//
// The report is intentionally a hint, not a hard error. The hook still
// runs whatever logic the binary has; we just surface a one-liner so
// the user knows to `go build` if they see weird old behaviour. The
// dispatcher prepends the Reason text into the cursor session_start
// additional_context, where the agent (which DOES have an LLM) can
// decide whether to flag it to the user.
type BinaryStalenessReport struct {
	Stale          bool   `json:"stale"`
	BinaryPath     string `json:"binary_path"`
	BinaryMtime    string `json:"binary_mtime,omitempty"`
	MainHeadCommit string `json:"main_head_commit,omitempty"`
	MainHeadAt     string `json:"main_head_at,omitempty"`
	HoursBehind    int    `json:"hours_behind,omitempty"`
	Reason         string `json:"reason,omitempty"`
}

// IsStale satisfies the structural interface the dispatcher uses to
// detect stale-binary reports without importing the engine package.
// Returning false on a nil receiver lets callers skip nil-checks.
func (r *BinaryStalenessReport) IsStale() bool {
	return r != nil && r.Stale
}

// StaleReason satisfies the same structural interface.
func (r *BinaryStalenessReport) StaleReason() string {
	if r == nil {
		return ""
	}
	return r.Reason
}

// stalenessTolerance is the safety margin on mtime comparison. Clock
// skew between the build machine and the commit author's machine can
// push a freshly-built binary's mtime a minute or two behind the main
// HEAD commit time without the binary actually being stale. 5 minutes
// is way bigger than realistic skew and small enough that a real
// "stale by an hour" binary still trips the check.
const stalenessTolerance = 5 * time.Minute

// BinaryStaleness returns a one-shot snapshot of how old the running
// mainline binary is relative to the synced main HEAD. Mechanical only:
// no semantic judgement, just file mtime + git commit date. Safe to
// expose on the hook EngineFacade per the "narrow surface" rule —
// adding a third pure mechanical method does not let hooks make
// semantic decisions.
//
// Failure modes are absorbed and reported via Reason rather than
// returned as Go errors: a stale-binary check that itself errors at
// session start should never block the session. Callers always get a
// non-nil report (even if it is a no-op one).
func (s *Service) BinaryStaleness() (*BinaryStalenessReport, error) {
	if err := s.requireInit(); err != nil {
		return &BinaryStalenessReport{Reason: fmt.Sprintf("not initialized: %v", err)}, nil
	}

	rep := &BinaryStalenessReport{}

	bin, err := os.Executable()
	if err != nil {
		rep.Reason = fmt.Sprintf("cannot resolve binary path: %v", err)
		return rep, nil
	}
	rep.BinaryPath = bin

	info, err := os.Stat(bin)
	if err != nil {
		rep.Reason = fmt.Sprintf("cannot stat binary: %v", err)
		return rep, nil
	}
	binMtime := info.ModTime()
	rep.BinaryMtime = binMtime.UTC().Format(time.RFC3339)

	cfg, _ := s.getTeamConfig()
	if cfg == nil {
		return rep, nil
	}

	mainRef := s.syncedMainRef(cfg.Mainline.MainBranch)
	headHash := s.Git.ReadRef(mainRef)
	if headHash == "" {
		return rep, nil
	}
	rep.MainHeadCommit = headHash

	headDate, err := s.Git.CommitDate(headHash)
	if err != nil || headDate == "" {
		return rep, nil
	}
	rep.MainHeadAt = headDate
	headTime, err := time.Parse(time.RFC3339, headDate)
	if err != nil {
		return rep, nil
	}

	delta := headTime.Sub(binMtime)
	if delta <= stalenessTolerance {
		return rep, nil
	}

	rep.Stale = true
	rep.HoursBehind = int(delta.Hours())
	rep.Reason = fmt.Sprintf(
		"binary at %s was built %s before main HEAD %s (committed at %s). "+
			"`go pull` since the last build can land bug fixes the running hook does not have — "+
			"rebuild with `go build -o %s .` if you see unexpected behaviour.",
		rep.BinaryPath,
		humanDelta(delta),
		shortHash(headHash),
		headTime.UTC().Format(time.RFC3339),
		rep.BinaryPath,
	)
	return rep, nil
}

func shortHash(h string) string {
	if len(h) >= 7 {
		return h[:7]
	}
	return h
}

// humanDelta formats a duration the way a developer expects to see it
// in a one-line warning ("3h", "2.4d"). RFC3339 vs RFC3339 subtraction
// produces sub-second precision that is noise in this context.
func humanDelta(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%.1fh", d.Hours())
	default:
		return fmt.Sprintf("%.1fd", d.Hours()/24)
	}
}
