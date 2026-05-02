package cli

import "testing"

func TestPreflightCommandIsDailyAndAutoSynced(t *testing.T) {
	if preflightCmd.GroupID != groupDaily.ID {
		t.Fatalf("preflight command should be in daily group, got %q", preflightCmd.GroupID)
	}
	if !autoSyncCommands["preflight"] {
		t.Fatal("preflight should reuse the auto-sync freshness window")
	}
	if _, _, err := rootCmd.Find([]string{"preflight", "--json"}); err != nil {
		t.Fatalf("preflight command should be registered on root: %v", err)
	}
}
