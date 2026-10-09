package claude

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// Expected bytes are independent native-runtime observations, including actual
// Haiku 5.5 bodies and model/token/Unicode mutations (CC-20261009-012).
func TestCCH2295NativeVectors(t *testing.T) {
	data, err := os.ReadFile("testdata/cch-2.1.295.json")
	require.NoError(t, err)
	var fixture struct {
		Vectors []struct{ Name, Body, Expected string }
	}
	require.NoError(t, json.Unmarshal(data, &fixture))
	require.Len(t, fixture.Vectors, 12)
	for _, v := range fixture.Vectors {
		t.Run(v.Name, func(t *testing.T) {
			actual, ok := CCH2292([]byte(v.Body))
			require.True(t, ok)
			require.Equal(t, v.Expected, string(actual))
		})
	}
}
