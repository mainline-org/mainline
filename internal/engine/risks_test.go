package engine

import (
	"testing"

	"github.com/mainline-org/mainline/internal/domain"
)

func TestParseRiskID_Valid(t *testing.T) {
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
		iid, idx, err := ParseRiskID(tc.input)
		if err != nil {
			t.Errorf("ParseRiskID(%q) unexpected error: %v", tc.input, err)
			continue
		}
		if iid != tc.intentID || idx != tc.index {
			t.Errorf("ParseRiskID(%q) = (%q, %d), want (%q, %d)", tc.input, iid, idx, tc.intentID, tc.index)
		}
	}
}

func TestParseRiskID_Invalid(t *testing.T) {
	cases := []string{
		"",
		"int_abc",
		"int_abc#",
		"abc#0",
		"int_XYZ#0", // uppercase not matching [0-9a-f]
		"#0",
		"int_abc#-1",
		"int_abc123#abc",
	}
	for _, tc := range cases {
		_, _, err := ParseRiskID(tc)
		if err == nil {
			t.Errorf("ParseRiskID(%q) expected error, got nil", tc)
		}
	}
}

func TestRiskID_Roundtrip(t *testing.T) {
	id := RiskID("int_abc123", 5)
	if id != "int_abc123#5" {
		t.Errorf("RiskID = %q, want %q", id, "int_abc123#5")
	}
	iid, idx, err := ParseRiskID(id)
	if err != nil {
		t.Fatalf("roundtrip failed: %v", err)
	}
	if iid != "int_abc123" || idx != 5 {
		t.Errorf("roundtrip mismatch: got (%q, %d)", iid, idx)
	}
}

func mkView(intents []domain.IntentView, resolutions map[string][]domain.RiskResolution) *domain.MainlineView {
	return &domain.MainlineView{
		Intents:         intents,
		RiskResolutions: resolutions,
	}
}

func mkIntent(id string, status domain.IntentStatus, risks []string, files []string) domain.IntentView {
	var fp *domain.SemanticFingerprint
	if len(files) > 0 {
		fp = &domain.SemanticFingerprint{FilesTouched: files}
	}
	return domain.IntentView{
		IntentID: id,
		Status:   status,
		SealedAt: "2025-01-01T00:00:00Z",
		Summary: &domain.IntentSummary{
			Risks: risks,
		},
		Fingerprint: fp,
	}
}

func TestMaterializeRisks_Open(t *testing.T) {
	view := mkView(
		[]domain.IntentView{
			mkIntent("int_aaa", domain.StatusMerged, []string{"risk one", "risk two"}, nil),
		},
		nil,
	)
	risks := materializeRisks(view, "")
	if len(risks) != 2 {
		t.Fatalf("expected 2 risks, got %d", len(risks))
	}
	for _, r := range risks {
		if r.Status != "open" {
			t.Errorf("risk %s should be open, got %s", r.ID, r.Status)
		}
	}
	if risks[0].ID != "int_aaa#0" || risks[1].ID != "int_aaa#1" {
		t.Errorf("unexpected IDs: %s, %s", risks[0].ID, risks[1].ID)
	}
}

func TestMaterializeRisks_Resolved(t *testing.T) {
	view := mkView(
		[]domain.IntentView{
			mkIntent("int_aaa", domain.StatusMerged, []string{"risk one", "risk two"}, nil),
		},
		map[string][]domain.RiskResolution{
			"int_aaa#0": {{IntentID: "int_bbb", Rationale: "fixed"}},
		},
	)
	risks := materializeRisks(view, "")
	if len(risks) != 2 {
		t.Fatalf("expected 2 risks, got %d", len(risks))
	}
	var resolved, open int
	for _, r := range risks {
		switch r.Status {
		case "resolved":
			resolved++
			if r.ID != "int_aaa#0" {
				t.Errorf("wrong risk resolved: %s", r.ID)
			}
		case "open":
			open++
		}
	}
	if resolved != 1 || open != 1 {
		t.Errorf("expected 1 resolved + 1 open, got %d resolved + %d open", resolved, open)
	}
}

func TestMaterializeRisks_Expired_Superseded(t *testing.T) {
	view := mkView(
		[]domain.IntentView{
			mkIntent("int_aaa", domain.StatusSuperseded, []string{"old risk"}, nil),
		},
		nil,
	)
	risks := materializeRisks(view, "")
	if len(risks) != 1 {
		t.Fatalf("expected 1 risk, got %d", len(risks))
	}
	if risks[0].Status != "expired" {
		t.Errorf("superseded intent's risk should be expired, got %s", risks[0].Status)
	}
}

func TestMaterializeRisks_Expired_Abandoned(t *testing.T) {
	view := mkView(
		[]domain.IntentView{
			mkIntent("int_aaa", domain.StatusAbandoned, []string{"abandoned risk"}, nil),
		},
		nil,
	)
	risks := materializeRisks(view, "")
	if len(risks) != 1 {
		t.Fatalf("expected 1 risk, got %d", len(risks))
	}
	if risks[0].Status != "expired" {
		t.Errorf("abandoned intent's risk should be expired, got %s", risks[0].Status)
	}
}

func TestMaterializeRisks_Expired_Reverted(t *testing.T) {
	view := mkView(
		[]domain.IntentView{
			mkIntent("int_aaa", domain.StatusReverted, []string{"reverted risk"}, nil),
		},
		nil,
	)
	risks := materializeRisks(view, "")
	if len(risks) != 1 {
		t.Fatalf("expected 1 risk, got %d", len(risks))
	}
	if risks[0].Status != "expired" {
		t.Errorf("reverted intent's risk should be expired, got %s", risks[0].Status)
	}
}

func TestMaterializeRisks_Expired_OverridesResolved(t *testing.T) {
	// If an intent is superseded AND a risk was resolved, expiry wins.
	view := mkView(
		[]domain.IntentView{
			mkIntent("int_aaa", domain.StatusSuperseded, []string{"old risk"}, nil),
		},
		map[string][]domain.RiskResolution{
			"int_aaa#0": {{IntentID: "int_bbb", Rationale: "fixed"}},
		},
	)
	risks := materializeRisks(view, "")
	if len(risks) != 1 {
		t.Fatalf("expected 1 risk, got %d", len(risks))
	}
	if risks[0].Status != "expired" {
		t.Errorf("expired should override resolved, got %s", risks[0].Status)
	}
}

func TestMaterializeRisks_FileFilter(t *testing.T) {
	view := mkView(
		[]domain.IntentView{
			mkIntent("int_aaa", domain.StatusMerged, []string{"auth risk"}, []string{"internal/auth/handler.go"}),
			mkIntent("int_bbb", domain.StatusMerged, []string{"db risk"}, []string{"internal/db/migrate.go"}),
		},
		nil,
	)
	risks := materializeRisks(view, "internal/auth")
	if len(risks) != 1 {
		t.Fatalf("expected 1 risk with auth filter, got %d", len(risks))
	}
	if risks[0].SourceIntent != "int_aaa" {
		t.Errorf("wrong intent: %s", risks[0].SourceIntent)
	}
}

func TestMaterializeRisks_SortOrder(t *testing.T) {
	view := mkView(
		[]domain.IntentView{
			mkIntent("int_aaa", domain.StatusMerged, []string{"open risk"}, nil),
			mkIntent("int_bbb", domain.StatusSuperseded, []string{"expired risk"}, nil),
		},
		map[string][]domain.RiskResolution{},
	)
	// Add a third intent with a resolved risk
	view.Intents = append(view.Intents,
		mkIntent("int_ccc", domain.StatusMerged, []string{"resolved risk"}, nil),
	)
	view.RiskResolutions["int_ccc#0"] = []domain.RiskResolution{{Rationale: "done"}}

	risks := materializeRisks(view, "")
	if len(risks) != 3 {
		t.Fatalf("expected 3 risks, got %d", len(risks))
	}
	// Order: open < resolved < expired
	if risks[0].Status != "open" {
		t.Errorf("first should be open, got %s", risks[0].Status)
	}
	if risks[1].Status != "resolved" {
		t.Errorf("second should be resolved, got %s", risks[1].Status)
	}
	if risks[2].Status != "expired" {
		t.Errorf("third should be expired, got %s", risks[2].Status)
	}
}

func TestMaterializeRisks_NilView(t *testing.T) {
	risks := materializeRisks(nil, "")
	if risks != nil {
		t.Errorf("nil view should return nil, got %v", risks)
	}
}

func TestMaterializeRisks_NoSummary(t *testing.T) {
	view := &domain.MainlineView{
		Intents: []domain.IntentView{
			{IntentID: "int_aaa", Status: domain.StatusMerged, Summary: nil},
		},
	}
	risks := materializeRisks(view, "")
	if len(risks) != 0 {
		t.Errorf("nil summary should produce no risks, got %d", len(risks))
	}
}

func TestMaterializeOpenRisks(t *testing.T) {
	view := mkView(
		[]domain.IntentView{
			mkIntent("int_aaa", domain.StatusMerged, []string{"auth risk"}, []string{"internal/auth/handler.go"}),
			mkIntent("int_bbb", domain.StatusMerged, []string{"db risk"}, []string{"internal/db/migrate.go"}),
			mkIntent("int_ccc", domain.StatusSuperseded, []string{"old risk"}, []string{"internal/auth/handler.go"}),
		},
		nil,
	)
	// Only open risks on files overlapping with auth should appear.
	open := materializeOpenRisks(view, []string{"internal/auth/handler.go"})
	if len(open) != 1 {
		t.Fatalf("expected 1 open risk on auth files, got %d", len(open))
	}
	if open[0].SourceIntent != "int_aaa" {
		t.Errorf("wrong source intent: %s", open[0].SourceIntent)
	}
}

func TestFilesOverlap(t *testing.T) {
	cases := []struct {
		a       []string
		b       []string
		overlap bool
	}{
		{[]string{"a.go"}, []string{"a.go"}, true},
		{[]string{"a.go"}, []string{"b.go"}, false},
		{[]string{"internal/auth/handler.go"}, []string{"internal/auth/middleware.go"}, true}, // same directory
		{[]string{"internal/auth/handler.go"}, []string{"internal/db/migrate.go"}, false},
		{nil, nil, false},
		{[]string{"a.go"}, nil, false},
	}
	for _, tc := range cases {
		got := filesOverlap(tc.a, tc.b)
		if got != tc.overlap {
			t.Errorf("filesOverlap(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.overlap)
		}
	}
}

func TestFilterOpenRisks_AllOpen(t *testing.T) {
	risks := []string{"risk one", "risk two"}
	result := filterOpenRisks("int_aaa", risks, nil, domain.StatusMerged)
	if len(result) != 2 {
		t.Errorf("all open: expected 2, got %d", len(result))
	}
}

func TestFilterOpenRisks_Resolved(t *testing.T) {
	risks := []string{"risk one", "risk two"}
	resolutions := map[string][]domain.RiskResolution{
		"int_aaa#0": {{Rationale: "fixed"}},
	}
	result := filterOpenRisks("int_aaa", risks, resolutions, domain.StatusMerged)
	if len(result) != 1 {
		t.Fatalf("expected 1 open after resolution, got %d", len(result))
	}
	if result[0] != "risk two" {
		t.Errorf("wrong remaining risk: %s", result[0])
	}
}

func TestFilterOpenRisks_Expired(t *testing.T) {
	risks := []string{"risk one"}
	result := filterOpenRisks("int_aaa", risks, nil, domain.StatusSuperseded)
	if len(result) != 0 {
		t.Errorf("expired source should return nil, got %d", len(result))
	}
}

func TestFilterOpenRisks_ExpiredAbandoned(t *testing.T) {
	risks := []string{"risk one"}
	result := filterOpenRisks("int_aaa", risks, nil, domain.StatusAbandoned)
	if len(result) != 0 {
		t.Errorf("abandoned source should return nil, got %d", len(result))
	}
}

func TestFilterOpenRisks_ExpiredReverted(t *testing.T) {
	risks := []string{"risk one"}
	result := filterOpenRisks("int_aaa", risks, nil, domain.StatusReverted)
	if len(result) != 0 {
		t.Errorf("reverted source should return nil, got %d", len(result))
	}
}

func TestStatusOrder(t *testing.T) {
	if statusOrder("open") >= statusOrder("resolved") {
		t.Error("open should sort before resolved")
	}
	if statusOrder("resolved") >= statusOrder("expired") {
		t.Error("resolved should sort before expired")
	}
}
