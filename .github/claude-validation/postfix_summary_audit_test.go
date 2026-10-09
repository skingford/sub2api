//go:build unit

package service

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPostfixSummarySequence(t *testing.T) {
	root, out := os.Getenv("CLAUDE_POSTFIX_SUMMARY_INPUT"), os.Getenv("CLAUDE_POSTFIX_SUMMARY_OUTPUT")
	if root == "" || out == "" {
		t.Skip("native summary captures required")
	}
	var result []map[string]any
	files, e := filepath.Glob(filepath.Join(root, "*", "requests.json"))
	require.NoError(t, e)
	for _, file := range files {
		var records []claudeAlignmentRecord
		b, e := os.ReadFile(file)
		require.NoError(t, e)
		require.NoError(t, json.Unmarshal(b, &records))
		var responses []struct {
			Index int    `json:"request_index"`
			Body  string `json:"body"`
		}
		b, e = os.ReadFile(filepath.Join(filepath.Dir(file), "responses.json"))
		require.NoError(t, e)
		require.NoError(t, json.Unmarshal(b, &responses))
		reply := map[int]string{}
		for _, v := range responses {
			reply[v.Index] = v.Body
		}
		m, store, svc, a := newAlignmentRecovery(t)
		attempt := 0
		for index, r := range records {
			if !strings.HasPrefix(r.Path, "/v1/messages?") {
				continue
			}
			attempt++
			uid := ParseMetadataUserID(gjson.Get(r.Raw, "metadata.user_id").String())
			require.NotNil(t, uid)
			scope := RecoveryScope{UserID: 1, GroupID: 7, ClientSession: uid.SessionID}
			ctx := alignmentContext(t, r)
			row := map[string]any{"case": filepath.Base(filepath.Dir(file)), "attempt": attempt, "request_index": index}
			p, err := m.Begin(ctx, svc, scope, []byte(r.Raw), "messages", "", false)
			if err != nil {
				row["error"] = err.Error()
				row["native_first_content"] = gjson.Get(r.Raw, "messages.0.content").Value()
				var saved RecoveryHistory
				require.NoError(t, m.cipher.open(store.rows[scope], "history", store.rows[scope].Material, &saved))
				if saved.Compaction != nil {
					row["stored_summary"] = saved.Compaction.Summary
				}
				result = append(result, row)
				break
			}
			require.NoError(t, p.BeforeSend(ctx, a))
			p.ResponseStatus.Store(200)
			require.NoError(t, p.Finish(ctx, 200, nil, []byte(reply[index]), true, ""))
			row["accepted"] = true
			result = append(result, row)
		}
	}
	b, e := json.MarshalIndent(result, "", "  ")
	require.NoError(t, e)
	require.NoError(t, os.WriteFile(out, append(b, '\n'), 0600))
}
