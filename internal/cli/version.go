package cli

import (
	"fmt"
	"regexp"
	"runtime/debug"
	"strings"
	"time"
)

// versionString describes the running binary, e.g.
//
//	drctl dev (commit 71d2dbb, committed 2026-09-28 22:03 UTC, go1.26.5)
//
// Go records the source commit and its date, not the time of the build: from
// the module version for "go install ...@version" (a pseudo-version such as
// v0.0.0-20260928220345-71d2dbb77da8 embeds both), or from git for a
// "go build" inside a clone, where uncommitted changes are flagged.
func versionString() string {
	info, _ := debug.ReadBuildInfo()
	return formatVersion(Version, info)
}

// A pseudo-version's last two parts are the UTC commit time and a 12-digit
// commit hash: v0.0.0-20260928220345-71d2dbb77da8, v1.2.4-0.20260928220345-71d2dbb77da8.
// Local builds get one from git too, with "+dirty" if there were uncommitted
// changes.
var pseudoVersion = regexp.MustCompile(`(\d{14})-([0-9a-f]{12})((?:\+[0-9A-Za-z.-]+)*)$`)

func formatVersion(version string, info *debug.BuildInfo) string {
	var commit, date, goVersion string
	modified := false
	if info != nil {
		goVersion = info.GoVersion
		mainVersion := info.Main.Version
		if m := pseudoVersion.FindStringSubmatch(mainVersion); m != nil {
			commit = m[2]
			modified = strings.Contains(m[3], "+dirty")
			if t, err := time.Parse("20060102150405", m[1]); err == nil {
				date = t.UTC().Format("2006-01-02 15:04 UTC")
			}
		} else if version == "dev" && mainVersion != "" && mainVersion != "(devel)" {
			version = mainVersion // a tagged release, e.g. go install ...@v0.1.0
		}
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				commit = s.Value
			case "vcs.time":
				if t, err := time.Parse(time.RFC3339, s.Value); err == nil {
					date = t.UTC().Format("2006-01-02 15:04 UTC")
				}
			case "vcs.modified":
				modified = s.Value == "true"
			}
		}
	}
	var details []string
	if commit != "" {
		if len(commit) > 7 {
			commit = commit[:7]
		}
		if modified {
			commit += " with uncommitted changes"
		}
		details = append(details, "commit "+commit)
	}
	if date != "" {
		details = append(details, "committed "+date)
	}
	if goVersion != "" {
		details = append(details, goVersion)
	}
	if len(details) == 0 {
		return "drctl " + version
	}
	return fmt.Sprintf("drctl %s (%s)", version, strings.Join(details, ", "))
}
