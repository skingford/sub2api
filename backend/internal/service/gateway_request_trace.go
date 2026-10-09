package service

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/pkg/requesttrace"
)

func traceClaudeForward(ctx context.Context, account *Account, operation string) func(*ForwardResult, error) {
	trace := requesttrace.FromContext(ctx)
	if trace == nil || account == nil {
		return func(*ForwardResult, error) {}
	}
	// Never serialize Account, Credentials or Extra wholesale.
	trace.Event("claude.account", map[string]any{
		"operation": operation, "account_id": account.ID, "platform": account.Platform, "auth_type": account.Type,
		"status": account.Status, "schedulable": account.Schedulable, "proxy_id": account.ProxyID,
		"concurrency": account.Concurrency, "last_used_at": account.LastUsedAt, "expires_at": account.ExpiresAt,
		"rate_limited_at": account.RateLimitedAt, "rate_limit_reset_at": account.RateLimitResetAt,
		"overload_until": account.OverloadUntil, "temp_unschedulable_until": account.TempUnschedulableUntil,
		"session_window_start": account.SessionWindowStart, "session_window_end": account.SessionWindowEnd,
	})
	return func(result *ForwardResult, err error) {
		fields := map[string]any{"operation": operation, "account_id": account.ID, "error": sanitizeUpstreamErrorMessage(requesttrace.SafeError(err)), "result_available": result != nil}
		if result != nil {
			fields["upstream_request_id"], fields["usage"] = result.RequestID, result.Usage
			fields["model"], fields["upstream_model"], fields["response_model"] = result.Model, result.UpstreamModel, result.UpstreamResponseModel
			fields["first_token_ms"], fields["duration_ms"] = result.FirstTokenMs, result.Duration.Milliseconds()
			fields["client_disconnect"], fields["stream"] = result.ClientDisconnect, result.Stream
		}
		trace.Event("claude.forward_end", fields)
	}
}
