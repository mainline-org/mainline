package buildinfo

import "runtime/debug"

var (
	version = ""
	commit  = ""
	date    = ""

	readBuildInfo = debug.ReadBuildInfo
)

type Info struct {
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
	Date    string `json:"date,omitempty"`
}

func Current() Info {
	return Info{
		Version: resolveVersion(),
		Commit:  resolveCommit(),
		Date:    resolveDate(),
	}
}

func resolveVersion() string {
	if version != "" {
		return version
	}
	if bi, ok := readBuildInfo(); ok {
		if bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			return bi.Main.Version
		}
	}
	return "dev"
}

func resolveCommit() string {
	if commit != "" {
		return shortCommit(commit)
	}
	if bi, ok := readBuildInfo(); ok {
		if v := buildSetting(bi, "vcs.revision"); v != "" {
			return shortCommit(v)
		}
	}
	return ""
}

func resolveDate() string {
	if date != "" {
		return date
	}
	if bi, ok := readBuildInfo(); ok {
		return buildSetting(bi, "vcs.time")
	}
	return ""
}

func buildSetting(bi *debug.BuildInfo, key string) string {
	if bi == nil {
		return ""
	}
	for _, setting := range bi.Settings {
		if setting.Key == key {
			return setting.Value
		}
	}
	return ""
}

func shortCommit(v string) string {
	if len(v) > 12 {
		return v[:12]
	}
	return v
}
