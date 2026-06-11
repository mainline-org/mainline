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

func TestSealStructuredSignalsFlagIsDeprecatedButParsed(t *testing.T) {
	if flag := sealCmd.Flags().Lookup("allow-structured-signals"); flag == nil {
		t.Fatal("deprecated --allow-structured-signals flag should remain parseable for migration errors")
	}
}

func TestActorImportCommandIsRegistered(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"actor", "import"})
	if err != nil || cmd.Name() != "import" {
		t.Fatalf("actor import command missing: cmd=%v err=%v", cmd, err)
	}
	for _, name := range []string{"actor", "remote", "source-ref", "import-ref", "force"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Fatalf("actor import missing --%s flag", name)
		}
	}
}

func TestPRImportCommandIsRegistered(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"pr-import"})
	if err != nil || cmd.Name() != "pr-import" {
		t.Fatalf("pr-import command missing: cmd=%v err=%v", cmd, err)
	}
	for _, name := range []string{"pr", "fork-url", "head-ref", "head-sha", "actor"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Fatalf("pr-import missing --%s flag", name)
		}
	}
}

func TestPublishCommandHasForkRemoteFlag(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"publish"})
	if err != nil || cmd.Name() != "publish" {
		t.Fatalf("publish command missing: cmd=%v err=%v", cmd, err)
	}
	if cmd.Flags().Lookup("remote") == nil {
		t.Fatal("publish missing --remote flag")
	}
}
