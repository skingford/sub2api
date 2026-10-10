//go:build unit

// Replays captured synthetic native responses through the production response
// converters. The source responses are independently assembled from SSE.
package service

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestClaudeResponseContentAudit(t *testing.T) {
	input, output := os.Getenv("CLAUDE_RESPONSE_FIXTURES"), os.Getenv("CLAUDE_RESPONSE_OUTPUT")
	if input == "" || output == "" {
		t.Skip("requires independently assembled response fixtures")
	}
	raw, err := os.ReadFile(input)
	require.NoError(t, err)
	var fixtures []struct {
		Name     string                           `json:"name"`
		Response apicompat.AnthropicResponse      `json:"response"`
		Events   []apicompat.AnthropicStreamEvent `json:"events"`
		SSE      string                           `json:"sse"`
	}
	require.NoError(t, json.Unmarshal(raw, &fixtures))
	var rows []map[string]any
	for _, fixture := range fixtures {
		whole := apicompat.AnthropicToResponsesResponse(&fixture.Response)
		state := apicompat.NewAnthropicEventToResponsesState()
		var events []apicompat.ResponsesStreamEvent
		for i := range fixture.Events {
			events = append(events, apicompat.AnthropicEventToResponsesEvents(&fixture.Events[i], state)...)
		}
		events = append(events, apicompat.FinalizeAnthropicResponsesStream(state)...)
		row := map[string]any{"name": fixture.Name, "source": fixture.Response, "nonstream": whole, "events": events}
		for _, event := range events {
			if event.Response != nil && (event.Type == "response.completed" || event.Type == "response.incomplete") {
				row["stream"] = event.Response
			}
		}
		for _, streaming := range []bool{false, true} {
			svc, _ := newClaudeContractGateway(t)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{}`))
			response := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(fixture.SSE))}
			var callErr error
			if streaming {
				_, callErr = svc.handleResponsesStreamingResponse(response, c, fixture.Response.Model, fixture.Response.Model, nil, time.Now(), apicompat.ResponsesClientToolMapping{})
				var gatewayEvents []apicompat.ResponsesStreamEvent
				for _, line := range strings.Split(rec.Body.String(), "\n") {
					if !strings.HasPrefix(line, "data: ") {
						continue
					}
					var event apicompat.ResponsesStreamEvent
					require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event))
					gatewayEvents = append(gatewayEvents, event)
					if event.Response != nil && (event.Type == "response.completed" || event.Type == "response.incomplete") {
						row["gateway_stream"] = event.Response
					}
				}
				row["gateway_events"] = gatewayEvents
			} else {
				_, callErr = svc.handleResponsesBufferedStreamingResponse(response, c, fixture.Response.Model, fixture.Response.Model, nil, time.Now(), apicompat.ResponsesClientToolMapping{})
				var result json.RawMessage = append([]byte(nil), rec.Body.Bytes()...)
				row["gateway_buffered"] = result
			}
			if callErr != nil {
				row[map[bool]string{false: "gateway_buffered_error", true: "gateway_stream_error"}[streaming]] = callErr.Error()
			}
		}
		rows = append(rows, row)
	}
	data, err := json.MarshalIndent(rows, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(output, append(data, '\n'), 0600))
}
