// Package buildinfo holds the version strings oocla reports.
package buildinfo

import "runtime/debug"

// Version is oocla's own version, printed by `oocla version` and reported to
// the CLI as the MCP tool server's version.
//
// It is a variable so a release build can stamp the tag into it:
//
//	go build -ldflags "-X github.com/kazufusa/oocla/internal/buildinfo.Version=v1.2.3"
var Version = "dev"

// Get returns the version to report. A binary built by `go install
// .../cmd/oocla@vX.Y.Z` carries no ldflags stamp, but the module version is
// recorded in the build info, so that is the fallback.
func Get() string {
	if Version != "dev" {
		return Version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return Version
}

// OllamaVersion is what /api/version reports. Clients gate features on it, so
// it has to be a plausible Ollama release rather than oocla's own version.
const OllamaVersion = "0.15.0"
