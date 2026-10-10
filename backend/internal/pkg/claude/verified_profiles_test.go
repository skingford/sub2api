package claude

import "testing"

func TestVerifiedCLIVersionAtOrBelow(t *testing.T) {
	for _, tc := range []struct{ discovered, want string }{
		{"2.1.292", "2.1.292"}, {"2.1.293", "2.1.292"},
		{"2.1.295", "2.1.295"}, {"2.1.300", "2.1.295"}, {"9.9.9", "2.1.295"},
		{"2.1.280", ""}, {"", ""}, {"garbage", ""}, {"2.1.295-beta", ""},
	} {
		if got := VerifiedCLIVersionAtOrBelow(tc.discovered); got != tc.want {
			t.Errorf("discovered %q: got %q, want %q", tc.discovered, got, tc.want)
		}
	}
}

func TestVerifiedCLIRegistryIsIsolatedAndContainsDefault(t *testing.T) {
	if !IsVerifiedCLIVersion(CLICurrentVersion) {
		t.Fatal("built-in default has no measured profile")
	}
	versions := VerifiedCLIVersions()
	first := versions[0]
	versions[0] = "9.9.9"
	if !IsVerifiedCLIVersion(first) || IsVerifiedCLIVersion("9.9.9") {
		t.Fatal("callers must not be able to alter the profile registry")
	}
}
