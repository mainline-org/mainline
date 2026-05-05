package cli

import "testing"

func TestSignalCommandsUsePluralQueues(t *testing.T) {
	if cmd, _, err := rootCmd.Find([]string{"risk"}); err == nil && cmd.Name() == "risk" {
		t.Fatalf("singular risk command should not be registered")
	}
	if cmd, _, err := rootCmd.Find([]string{"followup"}); err == nil && cmd.Name() == "followup" {
		t.Fatalf("singular followup command should not be registered")
	}

	if cmd, _, err := rootCmd.Find([]string{"risks", "add"}); err != nil || cmd.Name() != "add" {
		t.Fatalf("risks add command missing: cmd=%v err=%v", cmd, err)
	}
	if cmd, _, err := rootCmd.Find([]string{"followups", "add"}); err != nil || cmd.Name() != "add" {
		t.Fatalf("followups add command missing: cmd=%v err=%v", cmd, err)
	}
}
