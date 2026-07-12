package buildinfo

import "runtime/debug"

var (
	// Version is the semantic version. Set via ldflags: -X github.com/routatic/proxy/internal/buildinfo.Version=vX.Y.Z
	Version = "dev"
	// Commit is the git commit SHA. Set via ldflags: -X github.com/routatic/proxy/internal/buildinfo.Commit=<sha>
	Commit = "none"
	// Date is the build timestamp. Set via ldflags: -X github.com/routatic/proxy/internal/buildinfo.Date=YYYY-MM-DDTHH:MM:SSZ
	Date = "unknown"
)

func init() {
	// If built with `go install` or module-aware build and no ldflags provided,
	// try to extract version info from the embedded build metadata.
	if info, ok := debug.ReadBuildInfo(); ok {
		if Version == "dev" {
			if info.Main.Version != "" && info.Main.Version != "(devel)" {
				Version = info.Main.Version
			}
		}
		// Capture VCS info if present (Go 1.18+)
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				if Commit == "none" && s.Value != "" {
					Commit = s.Value
				}
			case "vcs.time":
				if Date == "unknown" && s.Value != "" {
					Date = s.Value
				}
			}
		}
	}
}

// String returns a human-readable build info summary.
func String() string {
	return Version + " (" + Commit + ") built at " + Date
}
