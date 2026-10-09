package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRequestTraceConfigDefaultsAndEnvironment(t *testing.T) {
	resetViperWithJWTSecret(t)
	cfg, err := Load()
	require.NoError(t, err)
	require.True(t, cfg.Gateway.RequestTrace.Enabled)
	require.Zero(t, cfg.Gateway.RequestTrace.MaxBodyBytes)
	require.Equal(t, 100, cfg.Gateway.RequestTrace.MaxBackups)
	t.Setenv("GATEWAY_REQUEST_TRACE_ENABLED", "false")
	t.Setenv("GATEWAY_REQUEST_TRACE_DIRECTORY", "/tmp/private-request-traces")
	t.Setenv("GATEWAY_REQUEST_TRACE_MAX_BODY_BYTES", "1024")
	cfg, err = Load()
	require.NoError(t, err)
	require.False(t, cfg.Gateway.RequestTrace.Enabled)
	require.Equal(t, int64(1024), cfg.Gateway.RequestTrace.MaxBodyBytes)
	require.Equal(t, "/tmp/private-request-traces", cfg.Gateway.RequestTrace.Directory)
}

func TestRequestTraceRejectsUnboundedRotation(t *testing.T) {
	base := RequestTraceConfig{Enabled: true, MaxSizeMB: 100, MaxBackups: 100, MaxAgeDays: 30}
	require.NoError(t, base.Validate())
	for _, change := range []func(*RequestTraceConfig){func(c *RequestTraceConfig) { c.MaxSizeMB = 0 }, func(c *RequestTraceConfig) { c.MaxBackups = 0 }, func(c *RequestTraceConfig) { c.MaxAgeDays = 0 }, func(c *RequestTraceConfig) { c.MaxBodyBytes = -1 }} {
		c := base
		change(&c)
		require.Error(t, c.Validate())
	}
}
