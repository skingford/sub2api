//go:build unit

package claude

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestComprehensiveCCHOracle(t *testing.T) {
	input, observed := os.Getenv("CLAUDE_CCH_CASES"), os.Getenv("CLAUDE_CCH_RECORDS")
	if input == "" || observed == "" {
		t.Skip("requires isolated native runtime oracle")
	}
	var cases []struct {
		Name, Body, Path string
		Version          *bool
	}
	var records []struct{ Name, Body string }
	b, e := os.ReadFile(input)
	require.NoError(t, e)
	require.NoError(t, json.Unmarshal(b, &cases))
	b, e = os.ReadFile(observed)
	require.NoError(t, e)
	require.NoError(t, json.Unmarshal(b, &records))
	require.Len(t, records, len(cases))
	for i, row := range cases {
		t.Run(row.Name, func(t *testing.T) {
			require.Equal(t, row.Name, records[i].Name)
			body := []byte(row.Body)
			if (row.Version == nil || *row.Version) && (row.Path == "" || row.Path == "/v1/messages?beta=true" || row.Path == "/v1/messages/count_tokens") {
				body, _ = CCH2292(body)
			}
			require.Equal(t, records[i].Body, string(body))
		})
	}
}
