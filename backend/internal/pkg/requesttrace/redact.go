package requesttrace

import (
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

func sensitiveKey(key string) bool {
	key = strings.ToLower(strings.ReplaceAll(key, "_", "-"))
	if key == "key" || key == "code" || key == "sig" || key == "signature" {
		return true
	}
	for _, word := range []string{"authorization", "cookie", "api-key", "apikey", "secret", "password", "credential", "signature"} {
		if strings.Contains(key, word) {
			return true
		}
	}
	if !strings.Contains(key, "token") {
		return false
	}
	// Quota counters are evidence, not authentication tokens.
	quota := strings.HasPrefix(key, "anthropic-ratelimit-") || strings.HasPrefix(key, "x-ratelimit-")
	return !quota || (!strings.HasSuffix(key, "-remaining") && !strings.HasSuffix(key, "-limit") && !strings.HasSuffix(key, "-reset"))
}

func Headers(h http.Header) http.Header {
	out := h.Clone()
	for k := range out {
		if sensitiveKey(k) {
			out[k] = []string{"[REDACTED]"}
			continue
		}
		switch strings.ToLower(k) {
		case "location", "content-location", "referer", "referrer":
			for i, raw := range out[k] {
				u, err := url.Parse(raw)
				if err != nil {
					out[k][i] = "[REDACTED_INVALID_URL]"
				} else {
					out[k][i] = URL(u)
				}
			}
		}
	}
	return out
}

func URL(u *url.URL) string {
	if u == nil {
		return ""
	}
	v := *u
	v.User = nil
	v.Fragment = ""
	parts := strings.Split(v.RawQuery, "&")
	for i, part := range parts {
		key, _, _ := strings.Cut(part, "=")
		decoded, err := url.QueryUnescape(key)
		if err != nil || strings.Contains(part, ";") {
			parts[i] = "[REDACTED_INVALID_QUERY]"
			continue
		}
		if sensitiveKey(decoded) {
			parts[i] = key + "=" + url.QueryEscape("[REDACTED]")
		}
	}
	v.RawQuery = strings.Join(parts, "&")
	return v.String()
}

func Proxy(raw string) string {
	if raw == "" {
		return "direct"
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "[INVALID_PROXY_URL]"
	}
	return URL(u)
}

var errorURL = regexp.MustCompile(`\b[a-zA-Z][a-zA-Z0-9+.-]*://[^\s"<>]+`)

func SafeError(err error) string {
	if err == nil {
		return ""
	}
	var uerr *url.Error
	if errors.As(err, &uerr) {
		err = uerr.Err
	}
	return errorURL.ReplaceAllStringFunc(err.Error(), func(raw string) string {
		u, parseErr := url.Parse(raw)
		if parseErr != nil {
			return "[REDACTED_URL]"
		}
		return URL(u)
	})
}
