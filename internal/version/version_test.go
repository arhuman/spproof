package version

import (
	"runtime/debug"
	"testing"
)

// buildInfo builds a *debug.BuildInfo with the module version and vcs settings
// the toolchain would embed.
func buildInfo(mainVersion string, settings ...debug.BuildSetting) *debug.BuildInfo {
	info := &debug.BuildInfo{Settings: settings}
	info.Main.Version = mainVersion
	return info
}

func setting(key, value string) debug.BuildSetting {
	return debug.BuildSetting{Key: key, Value: value}
}

func TestResolve(t *testing.T) {
	tests := []struct {
		name    string
		stamped Metadata
		info    *debug.BuildInfo
		want    Metadata
	}{
		{
			name:    "ldflags win over build info",
			stamped: Metadata{"v1.2.3", "abcdef1", "2026-01-02T03:04:05Z"},
			info:    buildInfo("v9.9.9", setting("vcs.revision", "0123456789abcdef"), setting("vcs.time", "2020-01-01T00:00:00Z")),
			want:    Metadata{"v1.2.3", "abcdef1", "2026-01-02T03:04:05Z"},
		},
		{
			name:    "unstamped falls back to vcs settings",
			stamped: Metadata{devVersion, unknownMeta, unknownMeta},
			info:    buildInfo("(devel)", setting("vcs.revision", "0123456789abcdef"), setting("vcs.time", "2026-03-04T05:06:07Z")),
			want:    Metadata{devVersion, "0123456", "2026-03-04T05:06:07Z"},
		},
		{
			name:    "dirty working tree marks the revision",
			stamped: Metadata{devVersion, unknownMeta, unknownMeta},
			info:    buildInfo("(devel)", setting("vcs.modified", "true"), setting("vcs.revision", "0123456789abcdef")),
			want:    Metadata{devVersion, "0123456-dirty", ""},
		},
		{
			name:    "module install takes the module version",
			stamped: Metadata{devVersion, unknownMeta, unknownMeta},
			info:    buildInfo("v0.4.1"),
			want:    Metadata{"v0.4.1", "", ""},
		},
		{
			name:    "empty stamped values behave as unstamped",
			stamped: Metadata{},
			info:    buildInfo("v0.4.1", setting("vcs.revision", "0123456789abcdef")),
			want:    Metadata{"v0.4.1", "0123456", ""},
		},
		{
			name:    "no build info keeps the stamped version alone",
			stamped: Metadata{devVersion, unknownMeta, unknownMeta},
			info:    nil,
			want:    Metadata{devVersion, "", ""},
		},
		{
			name:    "short revision is not truncated",
			stamped: Metadata{devVersion, unknownMeta, unknownMeta},
			info:    buildInfo("(devel)", setting("vcs.revision", "abc")),
			want:    Metadata{devVersion, "abc", ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolve(tt.stamped, tt.info)
			if got != tt.want {
				t.Fatalf("resolve() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestMetadataString(t *testing.T) {
	tests := []struct {
		name string
		m    Metadata
		want string
	}{
		{"full", Metadata{"v1.2.3", "abcdef1", "2026-01-02T03:04:05Z"}, "v1.2.3 (abcdef1, 2026-01-02T03:04:05Z)"},
		{"commit only", Metadata{"v1.2.3", "abcdef1", ""}, "v1.2.3 (abcdef1)"},
		{"date only", Metadata{"v1.2.3", "", "2026-01-02T03:04:05Z"}, "v1.2.3 (2026-01-02T03:04:05Z)"},
		{"version only", Metadata{"v1.2.3", "", ""}, "v1.2.3"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.m.String(); got != tt.want {
				t.Fatalf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestBuildNeverReportsPlaceholders pins the actual package-level value: the
// point of the fallback is that an unstamped test binary still resolves a
// commit from the embedded vcs settings.
func TestBuildNeverReportsPlaceholders(t *testing.T) {
	if Build.Commit == unknownMeta || Build.Date == unknownMeta {
		t.Fatalf("Build carries a placeholder: %+v", Build)
	}
	if Build.String() == "" {
		t.Fatal("Build renders as an empty string")
	}
}
