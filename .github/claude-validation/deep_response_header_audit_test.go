package responseheaders

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// Observe the configurable success-response policy, without classifying its
// intentional filtering as a newly introduced transport failure.
func TestDeepClaudeSuccessHeaderPolicy(t *testing.T) {
	path := os.Getenv("CLAUDE_HEADER_POLICY_OUTPUT")
	if path == "" {
		t.Skip("requires audit output")
	}
	source := http.Header{}
	source.Set("Content-Type", "application/json")
	source.Set("request-id", "req_local_success")
	source.Set("x-request-id", "local_generic_id")
	source.Set("anthropic-ratelimit-requests-remaining", "99")
	result := map[string]http.Header{}
	for _, additional := range []bool{false, true} {
		cfg := config.ResponseHeaderConfig{Enabled: true}
		name := "default"
		if additional {
			name = "explicit-additional"
			cfg.AdditionalAllowed = []string{"request-id", "anthropic-ratelimit-requests-remaining"}
		}
		result[name] = FilterHeaders(source, CompileHeaderFilter(cfg))
	}
	b, err := json.MarshalIndent(result, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, append(b, '\n'), 0600))
}
