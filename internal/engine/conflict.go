package engine

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/mainline-org/mainline/internal/domain"
)

// -----------------------------------------------------------
// Phase1 conflict detection (rc5).
// -----------------------------------------------------------
//
// Two surfaces feed in:
//   - Service.Sync: scans local active candidates (drafts + own
//     sealed/proposed) against newly-fetched remote proposed intents.
//   - Service.SealSubmit: scans the just-sealed fingerprint against
//     all merged + proposed mainline intents.
//
// Both surfaces share scoreCandidateAgainstRemote, which dispatches
// on whether the candidate has a full SemanticFingerprint or only a
// PartialFingerprint inferred from a draft.

// partialFingerprintThreshold is the lower bar applied when the
// candidate side is a PartialFingerprint. The full-fingerprint path
// uses cfg.Check.Phase1Threshold (0.10 in rc4); partial fingerprints
// carry less signal so we accept noisier matches in exchange for not
// silently missing the dominant case (file overlap on a known draft).
const partialFingerprintThreshold = 0.25

// stopwords is intentionally short — we only need to filter the most
// obviously meaningless tokens that show up in every goal/turn
// description. Anything more aggressive starts dropping legitimate
// signal (e.g. "auth" looks like a stopword by length but isn't).
var stopwords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "that": true,
	"this": true, "from": true, "into": true, "have": true, "will": true,
	"when": true, "where": true, "what": true, "which": true, "their": true, "there": true, "about": true, "should": true, "could": true,
	"would": true, "must": true, "after": true, "before": true,
	"because": true, "while": true, "between": true, "make": true,
	"made": true, "more": true, "less": true, "than": true, "then": true, "also": true, "only": true, "some": true, "very": true,
	"such": true, "each": true, "every": true, "other": true, "another": true, "fix": true, "add": true, "use": true, "uses": true,
	"used": true, "run": true, "runs": true, "via": true, "intent": true,
	"intents": true,
}

// keywordsFromText extracts a deduped lowercase token set suitable
// for jaccard. Tokens shorter than 4 chars are dropped (too generic),
// stopwords are dropped, non-letters stripped at boundaries.
func keywordsFromText(text string) []string {
	if text == "" {
		return nil
	}
	fields := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '_' && r != '-'
	})
	seen := make(map[string]bool, len(fields))
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.Trim(f, "_-")
		if len(f) < 4 || stopwords[f] || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// subsystemsFromFiles infers a coarse subsystem set from a list of
// file paths. The current rule: take the second path segment if the
// path lives under "internal/" (Go convention) or the first segment
// otherwise. Empty-result tolerated; conflict scoring already handles
// the empty-set case.
func subsystemsFromFiles(files []string) []string {
	seen := make(map[string]bool, len(files))
	var out []string
	for _, f := range files {
		f = filepath.ToSlash(f)
		parts := strings.Split(f, "/")
		var sub string
		switch {
		case len(parts) >= 2 && parts[0] == "internal":
			sub = parts[1]
		case len(parts) >= 1:
			sub = parts[0]
		}
		if sub == "" || seen[sub] {
			continue
		}
		seen[sub] = true
		out = append(out, sub)
	}
	sort.Strings(out)
	return out
}

// PartialFingerprintFromDraft builds the best-effort PartialFingerprint
// the conflict scorer needs from a DraftIntent. It reads only the
// committed draft state — turns, goal — and never touches git.
func PartialFingerprintFromDraft(d *domain.DraftIntent) *domain.PartialFingerprint {
	if d == nil {
		return nil
	}
	files := make(map[string]bool)
	textBuf := strings.Builder{}
	textBuf.WriteString(d.Goal)
	for _, t := range d.Turns {
		textBuf.WriteString(" ")
		textBuf.WriteString(t.Description)
		for _, fc := range t.FilesChanged {
			if fc.Path != "" {
				files[fc.Path] = true
			}
		}
	}
	fileList := make([]string, 0, len(files))
	for f := range files {
		fileList = append(fileList, f)
	}
	sort.Strings(fileList)
	return &domain.PartialFingerprint{
		FilesTouched: fileList,
		Keywords:     keywordsFromText(textBuf.String()),
		Subsystems:   subsystemsFromFiles(fileList),
		IsPartial:    true,
	}
}

// scorePartialAgainstFingerprint uses the rc5-spec weighted scheme:
// 0.40 file jaccard + 0.40 keyword jaccard + 0.20 subsystem jaccard.
// All inputs are sets; weights are biased toward file overlap because
// it is the highest-signal dimension a draft can populate honestly.
func scorePartialAgainstFingerprint(p *domain.PartialFingerprint, fp *domain.SemanticFingerprint) float64 {
	if p == nil || fp == nil {
		return 0
	}
	// Treat fingerprint goal-keyword equivalent as the union of its
	// architectural_claims / behavioral_changes / tags textual content.
	remoteKeywords := keywordsFromText(strings.Join(fp.ArchitecturalClaims, " ") + " " +
		strings.Join(fp.BehavioralChanges, " ") + " " +
		strings.Join(fp.Tags, " "))
	return 0.40*jaccard(p.FilesTouched, fp.FilesTouched) +
		0.40*jaccard(p.Keywords, remoteKeywords) +
		0.20*jaccard(p.Subsystems, fp.Subsystems)
}

// confidenceFor maps a phase1 score to one of three buckets the user
// sees in warning text and JSON. Boundaries are intentionally coarse:
// phase1 is a screen, not a measurement.
func confidenceFor(score float64, isPartial bool) string {
	switch {
	case isPartial:
		return "low"
	case score >= 0.40:
		return "high"
	case score >= 0.20:
		return "medium"
	default:
		return "low"
	}
}

// reasonFor produces a one-sentence human-readable explanation of why
// a particular pair scored. Used in stdout and the JSON conflict
// payload alike.
func reasonFor(localFiles, remoteFiles, localSubs, remoteSubs []string) string {
	fileOverlap := intersect(localFiles, remoteFiles)
	if len(fileOverlap) > 0 {
		head := fileOverlap[0]
		if len(fileOverlap) == 1 {
			return "shared file: " + head
		}
		return "shared files: " + head + " (+" + itoa(len(fileOverlap)-1) + " more)"
	}
	subOverlap := intersect(localSubs, remoteSubs)
	if len(subOverlap) > 0 {
		return "shared subsystem: " + subOverlap[0]
	}
	return "fingerprint overlap"
}

func intersect(a, b []string) []string {
	set := make(map[string]bool, len(a))
	for _, s := range a {
		set[s] = true
	}
	var out []string
	for _, s := range b {
		if set[s] {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// detectSealedConflicts is called from SealSubmit. It scores the
// freshly-sealed fingerprint against every other intent in the view
// (merged or proposed, candidate excluded) and returns warnings that
// pass cfg.Check.Phase1Threshold.
func (s *Service) detectSealedConflicts(candidateID string, fp *domain.SemanticFingerprint, view *domain.MainlineView, threshold float64) []domain.ConflictPair {
	if fp == nil || view == nil {
		return nil
	}
	var pairs []domain.ConflictPair
	for _, iv := range view.Intents {
		if iv.IntentID == candidateID || iv.Fingerprint == nil {
			continue
		}
		if iv.Status != domain.StatusMerged && iv.Status != domain.StatusProposed {
			continue
		}
		score := FingerprintOverlap(fp, iv.Fingerprint)
		if score < threshold {
			continue
		}
		pairs = append(pairs, domain.ConflictPair{
			LocalIntent:  candidateID,
			RemoteIntent: iv.IntentID,
			OverlapScore: score,
			Confidence:   confidenceFor(score, false),
			Reason: reasonFor(fp.FilesTouched, iv.Fingerprint.FilesTouched,
				fp.Subsystems, iv.Fingerprint.Subsystems),
			LocalSource:  "sealed",
			RemoteStatus: string(iv.Status),
		})
	}
	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].OverlapScore > pairs[j].OverlapScore
	})
	return pairs
}

// detectSyncConflicts is called from Sync. It scores every active
// local candidate (drafts + this actor's sealed/proposed intents)
// against every remote proposed intent in the view. Drafts use
// PartialFingerprint with the looser partial threshold; sealed
// candidates use the full FingerprintOverlap scorer.
//
// The function takes deltaSeenIDs — the set of intent ids new since
// the last sync — so it does not re-warn on pairs the user already
// saw on a previous sync. When deltaSeenIDs is nil, all view intents
// are eligible (used for the first sync after init or a hard refresh).
func (s *Service) detectSyncConflicts(view *domain.MainlineView, threshold float64, deltaSeenIDs map[string]bool) []domain.ConflictPair {
	if view == nil {
		return nil
	}
	identity, _ := s.getIdentity()
	myActor := ""
	if identity != nil {
		myActor = identity.ActorID
	}

	type candidate struct {
		id        string
		actor     string
		full      *domain.SemanticFingerprint
		partial   *domain.PartialFingerprint
		source    string // "draft" | "sealed"
		isPartial bool
		threshold float64
	}
	var candidates []candidate

	// Active drafts on disk (no SealResult yet → partial fingerprint).
	draftIDs, _ := s.Store.ListDrafts()
	for _, id := range draftIDs {
		d, _ := s.Store.ReadDraft(id)
		if d == nil || d.Status != domain.StatusDrafting {
			continue
		}
		p := PartialFingerprintFromDraft(d)
		if p == nil || (len(p.FilesTouched) == 0 && len(p.Keywords) == 0) {
			continue
		}
		candidates = append(candidates, candidate{
			id: d.IntentID, actor: myActor,
			partial: p, source: "draft",
			isPartial: true, threshold: partialFingerprintThreshold,
		})
	}

	// This actor's sealed/proposed intents in the view.
	for _, iv := range view.Intents {
		if iv.ActorID != myActor {
			continue
		}
		if iv.Fingerprint == nil {
			continue
		}
		if iv.Status != domain.StatusProposed && iv.Status != domain.StatusSealedLocal {
			continue
		}
		ivCopy := iv
		candidates = append(candidates, candidate{
			id: iv.IntentID, actor: iv.ActorID,
			full: ivCopy.Fingerprint, source: "sealed",
			isPartial: false, threshold: threshold,
		})
	}

	if len(candidates) == 0 {
		return nil
	}

	var pairs []domain.ConflictPair
	for _, iv := range view.Intents {
		// Pairs are interesting only when the remote side is *new*
		// since last sync; otherwise we already warned about it.
		if deltaSeenIDs != nil && !deltaSeenIDs[iv.IntentID] {
			continue
		}
		if iv.ActorID == myActor {
			continue
		}
		if iv.Fingerprint == nil {
			continue
		}
		if iv.Status != domain.StatusProposed && iv.Status != domain.StatusMerged {
			continue
		}
		for _, c := range candidates {
			if c.id == iv.IntentID {
				continue
			}
			var score float64
			if c.isPartial {
				score = scorePartialAgainstFingerprint(c.partial, iv.Fingerprint)
			} else {
				score = FingerprintOverlap(c.full, iv.Fingerprint)
			}
			if score < c.threshold {
				continue
			}
			var localFiles []string
			var localSubs []string
			if c.isPartial {
				localFiles = c.partial.FilesTouched
				localSubs = c.partial.Subsystems
			} else {
				localFiles = c.full.FilesTouched
				localSubs = c.full.Subsystems
			}
			pairs = append(pairs, domain.ConflictPair{
				LocalIntent:  c.id,
				RemoteIntent: iv.IntentID,
				OverlapScore: score,
				Confidence:   confidenceFor(score, c.isPartial),
				Reason: reasonFor(localFiles, iv.Fingerprint.FilesTouched,
					localSubs, iv.Fingerprint.Subsystems),
				LocalSource:  c.source,
				RemoteStatus: string(iv.Status),
			})
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].OverlapScore > pairs[j].OverlapScore
	})
	return pairs
}
