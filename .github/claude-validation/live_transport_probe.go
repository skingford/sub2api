// Run only in an internal Docker network with the restricted CONNECT proxy.
// The input is a synthetic production Forward export, never a user request.
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/repository"
)

func main() {
	if _, err := os.Stat("/.dockerenv"); err != nil {
		panic("requires isolated Docker")
	}
	host := os.Getenv("SUB2API_LIVE_AUTHORIZED_HOST")
	if host == "" || strings.ContainsAny(host, "/:@?#\\") {
		panic("explicit authorized HTTPS host required")
	}
	key, err := os.ReadFile("/run/secrets/relay-key")
	must(err)
	data, err := os.ReadFile(os.Args[1])
	must(err)
	var input struct {
		Path    string            `json:"path"`
		Headers map[string]string `json:"headers"`
		Wire    string            `json:"wire_body_base64"`
	}
	must(json.Unmarshal(data, &input))
	if input.Path != "/v1/messages" && input.Path != "/v1/messages?beta=true" {
		panic("unexpected endpoint")
	}
	wire, err := base64.StdEncoding.DecodeString(input.Wire)
	must(err)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", "https://"+host+input.Path, bytes.NewReader(wire))
	must(err)
	for key, value := range input.Headers {
		switch strings.ToLower(key) {
		case "authorization", "x-api-key", "cookie", "host", "content-length", "connection":
			continue
		}
		req.Header.Set(key, value)
	}
	req.Header.Set("x-api-key", strings.TrimSpace(string(key)))
	resp, err := repository.NewHTTPUpstream(nil).Do(req, "http://egress:8080", 292, 1)
	must(err)
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 65536))
	must(err)
	// Never print request headers or credentials, including error reflections.
	clean := strings.ReplaceAll(string(body), strings.TrimSpace(string(key)), "[REDACTED]")
	must(json.NewEncoder(os.Stdout).Encode(map[string]any{"status": resp.StatusCode, "body": clean, "protocol": resp.Proto}))
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "live probe failed; request credentials suppressed")
		os.Exit(1)
	}
}
