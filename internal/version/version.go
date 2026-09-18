// Package version reports which build of this binary is running.
package version

import "runtime/debug"

// Stamped at link time via -ldflags -X: see the Makefile's LDFLAGS. Exported
// because the linker addresses a variable by its qualified name, and can only
// set one initialised to a constant string.
var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildDate = "unknown"
)

// The values the three variables hold when nothing stamped them.
const (
	devVersion  = "dev"
	unknownMeta = "unknown"
)

// Build is what this binary reports about itself, resolved once at startup. It
// is the single source for every place a version is printed.
var Build = resolve(Metadata{Version, GitCommit, BuildDate}, readBuildInfo())

func readBuildInfo() *debug.BuildInfo {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return nil
	}
	return info
}

// Metadata is the version, commit and build date this binary reports. Commit
// and date are empty when the build carries nothing to fill them, and the
// rendered line then omits them rather than printing a placeholder.
type Metadata struct {
	Version string
	Commit  string
	Date    string
}

// resolve derives the reported metadata from the linker-stamped values, falling
// back to the build info the toolchain embeds. Split from the package-level
// variable so the fallbacks can be tested without relinking the test binary.
//
// info may be nil, which happens only for a binary built without module
// information at all.
func resolve(stamped Metadata, info *debug.BuildInfo) Metadata {
	m := stamped
	if m.Version == "" {
		m.Version = devVersion
	}
	if m.Commit == unknownMeta {
		m.Commit = ""
	}
	if m.Date == unknownMeta {
		m.Date = ""
	}
	if info == nil {
		return m
	}
	if m.Version == devVersion && info.Main.Version != "" && info.Main.Version != "(devel)" {
		m.Version = info.Main.Version
	}
	revision, date := vcsStamp(info)
	if m.Commit == "" {
		m.Commit = revision
	}
	if m.Date == "" {
		m.Date = date
	}
	return m
}

// vcsStamp extracts the short revision (with a dirty marker) and the commit
// time the toolchain embeds for a working-tree build. Both are empty for a
// module install, which carries no VCS information at all.
func vcsStamp(info *debug.BuildInfo) (revision, date string) {
	var modified bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = shortRevision(s.Value)
		case "vcs.time":
			date = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	// Appended after the loop, not inside it: the settings carry no ordering
	// guarantee, and the dirty marker would be lost if it came second.
	if modified && revision != "" {
		revision += "-dirty"
	}
	return revision, date
}

// shortRevision abbreviates a commit hash to the width the Makefile stamps, so
// every build path prints the same shape.
func shortRevision(rev string) string {
	const short = 7
	if len(rev) <= short {
		return rev
	}
	return rev[:short]
}

// String renders the metadata as it appears after the program name. A missing
// commit or date is omitted rather than printed as a placeholder.
func (m Metadata) String() string {
	switch {
	case m.Commit == "" && m.Date == "":
		return m.Version
	case m.Date == "":
		return m.Version + " (" + m.Commit + ")"
	case m.Commit == "":
		return m.Version + " (" + m.Date + ")"
	default:
		return m.Version + " (" + m.Commit + ", " + m.Date + ")"
	}
}
