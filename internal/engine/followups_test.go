package engine

import (
	"testing"

	"github.com/mainline-org/mainline/internal/domain"
)

func TestParseFollowupID_Valid(t *testing.T) {
	cases := []struct {
		input    string
		intentID string
		index    int
	}{
		{"int_abc123#0", "int_abc123", 0},
		{"int_0f0f0f#3", "int_0f0f0f", 3},
		{"int_deadbeef#12", "int_deadbeef", 12},
	}
	for _, tc := range cases {
		iid, idx, err := ParseFollowupID(tc.input)
		if err != nil {
			t.Errorf("ParseFollowupID(%q) unexpected error: %v", tc.input, err)
			continue
		}
		if iid != tc.intentID || idx != tc.index {
			t.Errorf("ParseFollowupID(%q) = (%q, %d), want (%q, %d)", tc.input, iid, idx, tc.intentID, tc.index)
		}
	}
}

func TestParseFollowupID_Invalid(t *testing.T) {
	cases := []string{
		"",
		"int_abc",
		"int_abc#",
		"abc#0",
		"int_XYZ#0",
		"#0",
		"int_abc#-1",
		"int_abc123#abc",
	}
	for _, tc := range cases {
		_, _, err := ParseFollowupID(tc)
		if err == nil {
			t.Errorf("ParseFollowupID(%q) expected error, got nil", tc)
		}
	}
}

func TestFollowupID_Roundtrip(t *testing.T) {
	id := FollowupID("int_abc123", 5)
	if id != "int_abc123#5" {
		t.Errorf("FollowupID = %q, want %q", id, "int_abc123#5")
	}
	iid, idx, err := ParseFollowupID(id)
	if err != nil {
		t.Fatalf("roundtrip failed: %v", err)
	}
	if iid != "int_abc123" || idx != 5 {
		t.Errorf("roundtrip mismatch: got (%q, %d)", iid, idx)
	}
}

func mkFollowupIntent(id string, status domain.IntentStatus, followups []string, files []string) domain.IntentView {
	var fp *domain.SemanticFingerprint
	if len(files) > 0 {
		fp = &domain.SemanticFingerprint{FilesTouched: files}
	}
	return domain.IntentView{
		IntentID: id,
		Status:   status,
		SealedAt: "2025-01-01T00:00:00Z",
		Summary: &domain.IntentSummary{
			Followups: domain.LegacyFollowupStatements(followups...),
		},
		Fingerprint: fp,
	}
}

func TestMaterializeFollowups_Open(t *testing.T) {
	view := &domain.MainlineView{
		Intents: []domain.IntentView{
			mkFollowupIntent("int_aaa", domain.StatusMerged, []string{"follow-up one", "follow-up two"}, nil),
		},
	}
	followups := materializeFollowups(view, "")
	if len(followups) != 2 {
		t.Fatalf("expected 2 follow-ups, got %d", len(followups))
	}
	for _, f := range followups {
		if f.Status != "open" {
			t.Errorf("follow-up %s should be open, got %s", f.ID, f.Status)
		}
	}
	if followups[0].ID != "int_aaa#0" || followups[1].ID != "int_aaa#1" {
		t.Errorf("unexpected IDs: %s, %s", followups[0].ID, followups[1].ID)
	}
}

func TestMaterializeFollowups_Resolved(t *testing.T) {
	view := &domain.MainlineView{
		Intents: []domain.IntentView{
			mkFollowupIntent("int_aaa", domain.StatusMerged, []string{"follow-up one", "follow-up two"}, nil),
		},
		FollowupResolutions: map[string][]domain.FollowupResolution{
			"int_aaa#0": {{IntentID: "int_bbb", Rationale: "done"}},
		},
	}
	followups := materializeFollowups(view, "")
	if len(followups) != 2 {
		t.Fatalf("expected 2 follow-ups, got %d", len(followups))
	}
	var resolved, open int
	for _, f := range followups {
		switch f.Status {
		case "resolved":
			resolved++
			if f.ID != "int_aaa#0" {
				t.Errorf("wrong follow-up resolved: %s", f.ID)
			}
		case "open":
			open++
		}
	}
	if resolved != 1 || open != 1 {
		t.Errorf("expected 1 resolved + 1 open, got %d resolved + %d open", resolved, open)
	}
}

func TestMaterializeFollowups_Expired(t *testing.T) {
	view := &domain.MainlineView{
		Intents: []domain.IntentView{
			mkFollowupIntent("int_aaa", domain.StatusSuperseded, []string{"old follow-up"}, nil),
		},
		FollowupResolutions: map[string][]domain.FollowupResolution{
			"int_aaa#0": {{IntentID: "int_bbb", Rationale: "done"}},
		},
	}
	followups := materializeFollowups(view, "")
	if len(followups) != 1 {
		t.Fatalf("expected 1 follow-up, got %d", len(followups))
	}
	if followups[0].Status != "expired" {
		t.Errorf("expired should override resolved, got %s", followups[0].Status)
	}
}

func TestMaterializeFollowups_FileFilter(t *testing.T) {
	view := &domain.MainlineView{
		Intents: []domain.IntentView{
			mkFollowupIntent("int_aaa", domain.StatusMerged, []string{"auth follow-up"}, []string{"internal/auth/handler.go"}),
			mkFollowupIntent("int_bbb", domain.StatusMerged, []string{"db follow-up"}, []string{"internal/db/migrate.go"}),
		},
	}
	followups := materializeFollowups(view, "internal/auth")
	if len(followups) != 1 {
		t.Fatalf("expected 1 follow-up with auth filter, got %d", len(followups))
	}
	if followups[0].SourceIntent != "int_aaa" {
		t.Errorf("wrong intent: %s", followups[0].SourceIntent)
	}
}

func TestMaterializeOpenFollowups(t *testing.T) {
	view := &domain.MainlineView{
		Intents: []domain.IntentView{
			mkFollowupIntent("int_aaa", domain.StatusMerged, []string{"auth follow-up"}, []string{"internal/auth/handler.go"}),
			mkFollowupIntent("int_bbb", domain.StatusMerged, []string{"db follow-up"}, []string{"internal/db/migrate.go"}),
			mkFollowupIntent("int_ccc", domain.StatusSuperseded, []string{"old follow-up"}, []string{"internal/auth/handler.go"}),
		},
	}
	open := materializeOpenFollowups(view, []string{"internal/auth/handler.go"})
	if len(open) != 1 {
		t.Fatalf("expected 1 open follow-up on auth files, got %d", len(open))
	}
	if open[0].SourceIntent != "int_aaa" {
		t.Errorf("wrong source intent: %s", open[0].SourceIntent)
	}
}

func TestFilterOpenFollowups_Resolved(t *testing.T) {
	followups := domain.LegacyFollowupStatements("follow-up one", "follow-up two")
	resolutions := map[string][]domain.FollowupResolution{
		"int_aaa#0": {{Rationale: "done"}},
	}
	result := filterOpenFollowups("int_aaa", followups, resolutions, domain.StatusMerged)
	if len(result) != 1 {
		t.Fatalf("expected 1 open after resolution, got %d", len(result))
	}
	if result[0] != "follow-up two" {
		t.Errorf("wrong remaining follow-up: %s", result[0])
	}
}

func TestFilterOpenFollowups_Expired(t *testing.T) {
	followups := domain.LegacyFollowupStatements("follow-up one")
	result := filterOpenFollowups("int_aaa", followups, nil, domain.StatusAbandoned)
	if len(result) != 0 {
		t.Errorf("expired source should return nil, got %d", len(result))
	}
}
