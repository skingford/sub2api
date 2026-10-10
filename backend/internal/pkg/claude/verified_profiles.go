package claude

import (
	"strings"

	"golang.org/x/mod/semver"
)

// Curated versions with measured API-to-OAuth body, header, CCH and default
// Linux x64 transport profiles. A release number alone never adds a version.
// Adding an entry requires the source/capture ledger and gateway contract tests.
var verifiedCLIProfileVersions = [...]string{"2.1.292", "2.1.295"}

func VerifiedCLIVersions() []string {
	return append([]string(nil), verifiedCLIProfileVersions[:]...)
}

func IsVerifiedCLIVersion(version string) bool {
	for _, known := range verifiedCLIProfileVersions {
		if version == known {
			return true
		}
	}
	return false
}

// VerifiedCLIVersionAtOrBelow resolves an automatically discovered release to
// an available, measured profile. Empty means no such profile; the caller keeps
// its existing environment/built-in fallback rather than claiming that release.
func VerifiedCLIVersionAtOrBelow(discovered string) string {
	discovered = strings.TrimSpace(discovered)
	if !IsSupportedCLIVersion(discovered) {
		return ""
	}
	best := ""
	for _, version := range verifiedCLIProfileVersions {
		if semver.Compare("v"+version, "v"+discovered) <= 0 &&
			(best == "" || semver.Compare("v"+version, "v"+best) > 0) {
			best = version
		}
	}
	return best
}
