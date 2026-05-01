package buildinfo

import "runtime/debug"
import "testing"

func TestCurrentPrefersInjectedValues(t *testing.T) {
	prevVersion, prevCommit, prevDate := version, commit, date
	prevRead := readBuildInfo
	t.Cleanup(func() {
		version, commit, date = prevVersion, prevCommit, prevDate
		readBuildInfo = prevRead
	})

	version = "v0.4.0"
	commit = "1234567890abcdef"
	date = "2026-05-02T00:00:00Z"
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{
			Main: debug.Module{Version: "v9.9.9"},
			Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "fedcba0987654321"},
				{Key: "vcs.time", Value: "2026-01-01T00:00:00Z"},
			},
		}, true
	}

	got := Current()
	if got.Version != "v0.4.0" {
		t.Fatalf("version = %q, want injected v0.4.0", got.Version)
	}
	if got.Commit != "1234567890ab" {
		t.Fatalf("commit = %q, want injected short commit", got.Commit)
	}
	if got.Date != "2026-05-02T00:00:00Z" {
		t.Fatalf("date = %q, want injected date", got.Date)
	}
}

func TestCurrentFallsBackToBuildInfo(t *testing.T) {
	prevVersion, prevCommit, prevDate := version, commit, date
	prevRead := readBuildInfo
	t.Cleanup(func() {
		version, commit, date = prevVersion, prevCommit, prevDate
		readBuildInfo = prevRead
	})

	version, commit, date = "", "", ""
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{
			Main: debug.Module{Version: "v1.2.3"},
			Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "abcdef1234567890"},
				{Key: "vcs.time", Value: "2026-04-30T12:00:00Z"},
			},
		}, true
	}

	got := Current()
	if got.Version != "v1.2.3" {
		t.Fatalf("version = %q, want v1.2.3", got.Version)
	}
	if got.Commit != "abcdef123456" {
		t.Fatalf("commit = %q, want shortened vcs revision", got.Commit)
	}
	if got.Date != "2026-04-30T12:00:00Z" {
		t.Fatalf("date = %q, want vcs.time", got.Date)
	}
}

func TestCurrentUsesDevFallbackWithoutBuildInfo(t *testing.T) {
	prevVersion, prevCommit, prevDate := version, commit, date
	prevRead := readBuildInfo
	t.Cleanup(func() {
		version, commit, date = prevVersion, prevCommit, prevDate
		readBuildInfo = prevRead
	})

	version, commit, date = "", "", ""
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		return nil, false
	}

	got := Current()
	if got.Version != "dev" {
		t.Fatalf("version = %q, want dev", got.Version)
	}
	if got.Commit != "" {
		t.Fatalf("commit = %q, want empty", got.Commit)
	}
	if got.Date != "" {
		t.Fatalf("date = %q, want empty", got.Date)
	}
}
