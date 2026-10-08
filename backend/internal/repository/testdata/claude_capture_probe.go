package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func main() {
	routes, err := os.ReadFile("/proc/net/route")
	must(err)
	if len(strings.Split(strings.TrimSpace(string(routes)), "\n")) != 1 {
		panic("Capture probe requires an isolated Linux network namespace without routes")
	}
	if len(os.Args) < 3 || len(os.Args) > 4 {
		panic("usage: probe request.json default|fingerprint|native")
	}
	input, err := os.ReadFile(os.Args[1])
	must(err)
	var captured struct {
		Method  string            `json:"method"`
		Path    string            `json:"path"`
		Headers map[string]string `json:"headers"`
		Body    string            `json:"raw_body_utf8"`
		Profile string            `json:"upstream_profile"`
		Auth    string            `json:"synthetic_auth"`
	}
	must(json.Unmarshal(input, &captured))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if strings.HasPrefix(os.Args[2], "auto") {
		if captured.Profile != string(service.HTTPUpstreamProfileClaude2292) {
			panic("auto mode requires a profile selected by the service capture")
		}
		ctx = service.WithHTTPUpstreamProfile(ctx, service.HTTPUpstreamProfileClaude2292)
	}
	req, err := http.NewRequestWithContext(ctx, captured.Method, "https://api.anthropic.com"+captured.Path, bytes.NewBufferString(captured.Body))
	must(err)
	for key, value := range captured.Headers {
		switch strings.ToLower(key) {
		case "authorization", "x-api-key", "cookie", "host", "content-length", "connection":
			continue
		}
		req.Header[key] = []string{value}
	}
	if captured.Auth == "oauth" {
		req.Header["authorization"] = []string{"Bearer local-only-oauth-token-not-a-real-credential"}
	} else {
		req.Header["x-api-key"] = []string{"local-container-key-not-a-real-credential"}
	}
	var profile *tlsfingerprint.Profile
	if strings.HasPrefix(os.Args[2], "native") {
		profile = tlsfingerprint.ClaudeCode2292()
	}
	if os.Args[2] == "fingerprint" {
		profile = &tlsfingerprint.Profile{Name: "builtin-default"}
	}
	proxyURL := ""
	if len(os.Args) == 4 {
		proxyURL = os.Args[3]
	}
	response, err := repository.NewHTTPUpstream(nil).DoWithTLS(req, proxyURL, 292, 1, profile)
	must(err)
	defer response.Body.Close()
	_, err = io.Copy(io.Discard, response.Body)
	must(err)
	fmt.Printf("mode=%s status=%d protocol=%s\n", os.Args[2], response.StatusCode, response.Proto)
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}
