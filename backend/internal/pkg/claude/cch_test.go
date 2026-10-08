//go:build unit

package claude

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCCH2292NativeRuntimeVectors(t *testing.T) {
	raw, err := os.ReadFile("testdata/cch-2.1.292.json")
	require.NoError(t, err)
	var data struct {
		Vectors []struct {
			Name, Body, Path string
			Version          *bool
			Expected         string `json:"expected_body_sha256"`
		}
	}
	require.NoError(t, json.Unmarshal(raw, &data))
	require.Len(t, data.Vectors, 81)
	for _, v := range data.Vectors {
		t.Run(v.Name, func(t *testing.T) {
			input := []byte(v.Body)
			before := sha256.Sum256(input)
			out := input
			// These gates belong to the native HTTP layer, outside the hash routine.
			if (v.Version == nil || *v.Version) && (v.Path == "" || v.Path == "/v1/messages?beta=true" || v.Path == "/v1/messages/count_tokens") {
				out, _ = CCH2292(input)
			}
			got := sha256.Sum256(out)
			require.Equal(t, v.Expected, hex.EncodeToString(got[:]))
			require.Equal(t, before, sha256.Sum256(input), "input buffer must not change")
		})
	}
}
