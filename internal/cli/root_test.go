package cli

import (
	"testing"

	"github.com/mainline-org/mainline/internal/domain"
)

func TestAutoSyncEnabledHonorsTeamConfig(t *testing.T) {
	cfg := domain.DefaultTeamConfig()
	if !autoSyncEnabled(&cfg) {
		t.Fatal("default config should enable automatic sync")
	}
	cfg.Sync.AutoSync = false
	if autoSyncEnabled(&cfg) {
		t.Fatal("auto_sync=false should disable automatic sync")
	}
}

func TestLocalWorktreePreflightSkipsAutomaticNetworkSync(t *testing.T) {
	if !skipAutoSyncForCoordinationScope("preflight", "local_worktrees") {
		t.Fatal("local-worktree preflight should use local evidence without network sync")
	}
	if skipAutoSyncForCoordinationScope("check", "local_worktrees") {
		t.Fatal("delivery-time check still needs the shared view")
	}
	if skipAutoSyncForCoordinationScope("preflight", "team") {
		t.Fatal("team preflight should preserve automatic sync")
	}
}

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

func TestPRCommentCommandSupportsForkMetadata(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"pr-comment"})
	if err != nil || cmd.Name() != "pr-comment" {
		t.Fatalf("pr-comment command missing: cmd=%v err=%v", cmd, err)
	}
	for _, name := range []string{"base", "head", "branch", "pr", "fork-url"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Fatalf("pr-comment missing --%s flag", name)
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
