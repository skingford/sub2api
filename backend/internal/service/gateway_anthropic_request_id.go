package service

import (
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// ensureAnthropicClientRequestID supplies request correlation for the canonical
// HTTPS API origin. Claude Code 2.1.286 gates this behavior on the provider and
// base URL; a capture made against localhost does not exercise that branch.
// This ID is not a client-identity or entitlement proof. Custom relays retain
// their caller-provided headers without receiving a generated ID.
func ensureAnthropicClientRequestID(req *http.Request) {
	if req.URL.Scheme != "https" || req.URL.User != nil ||
		!strings.EqualFold(req.URL.Hostname(), "api.anthropic.com") ||
		(req.URL.Port() != "" && req.URL.Port() != "443") {
		return
	}
	for key := range req.Header {
		if strings.EqualFold(key, "x-client-request-id") {
			return
		}
	}
	setHeaderRaw(req.Header, "x-client-request-id", uuid.NewString())
}
