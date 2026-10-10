package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadClaudeOAuthPreserveCaller(t *testing.T) {
	for _, tc := range []struct {
		name, file, env string
		want            bool
	}{
		{name: "default", want: true},
		{name: "file", file: "true", want: true},
		{name: "environment", env: "true", want: true},
		{name: "rollback", file: "true", env: "false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetViperWithJWTSecret(t)
			t.Setenv("GATEWAY_CLAUDE_OAUTH_PRESERVE_CALLER", tc.env)
			if tc.file != "" {
				path := filepath.Join(t.TempDir(), "config.yaml")
				require.NoError(t, os.WriteFile(path, []byte("gateway:\n  claude_oauth_preserve_caller: "+tc.file+"\n"), 0600))
				t.Setenv("CONFIG_FILE", path)
			}
			cfg, err := Load()
			require.NoError(t, err)
			require.Equal(t, tc.want, cfg.Gateway.ClaudeOAuthPreserveCaller)
		})
	}
}
