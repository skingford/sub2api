package repository

import (
	"bytes"
	"encoding/base64"
	"net/http"
	"os"
	"strings"
	"testing"

	fhttp "github.com/bogdanfinn/fhttp"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestNativeClaudeGzipHeaderOrderMatchesUnmodifiedCLI(t *testing.T) {
	fixture, err := os.ReadFile("../service/testdata/claude_code_2_1_292/parameter_alignment/native-gzip.json")
	require.NoError(t, err)
	wire, err := base64.StdEncoding.DecodeString(gjson.GetBytes(fixture, "wire_body_base64").String())
	require.NoError(t, err)
	for _, lowerEncoding := range []bool{false, true} {
		req, err := http.NewRequest(http.MethodPost, "https://api.anthropic.com/v1/messages?beta=true", bytes.NewReader(wire))
		require.NoError(t, err)
		var expected []string
		gjson.GetBytes(fixture, "headers").ForEach(func(key, value gjson.Result) bool {
			name := key.String()
			expected = append(expected, strings.ToLower(name))
			if lowerEncoding && name == "Content-Encoding" {
				name = "content-encoding"
			}
			req.Header[name] = []string{value.String()}
			return true
		})
		actual := nativeClaudeHeaders(req)
		require.Equal(t, expected, actual[fhttp.HeaderOrderKey], "order comes from the independent native capture")
		require.Equal(t, []string{"gzip"}, actual["Content-Encoding"])
	}
}
