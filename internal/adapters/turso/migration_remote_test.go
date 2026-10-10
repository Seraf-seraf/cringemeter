package turso

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Exercise the real HTTP driver: non-transactional requests close their stream.
func TestRemoteMigrationKeepsTransactionAndRollsBackFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "rollback"}[fail], func(t *testing.T) {
			version, pendingVersion := 0, 0
			transaction, committed, rolledBack := false, false, false
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var input struct {
					Baton    string `json:"baton"`
					Requests []struct {
						Type string `json:"type"`
						Stmt struct {
							SQL string `json:"sql"`
						} `json:"stmt"`
					} `json:"requests"`
				}
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
					t.Error(err)
					return
				}
				results := []any{}
				for _, request := range input.Requests {
					if request.Type == "close" {
						results = append(results, map[string]any{"type": "ok", "response": map[string]any{"type": "close"}})
						continue
					}
					if request.Type != "execute" {
						t.Error("expected individual SQL statement")
						http.Error(w, "unsupported", 400)
						return
					}
					sql := strings.TrimSpace(request.Stmt.SQL)
					if transaction && input.Baton != "transaction" {
						t.Error("lost transaction stream")
					}
					cols, rows := []any{}, []any{}
					switch {
					case sql == "BEGIN IMMEDIATE":
						transaction = true
					case sql == "PRAGMA user_version":
						cols = []any{map[string]any{"name": "user_version"}}
						rows = []any{[]any{map[string]any{"type": "integer", "value": map[int]string{0: "0", 1: "1"}[version]}}}
					case sql == "PRAGMA user_version=1":
						pendingVersion = 1
					case sql == "COMMIT":
						version = pendingVersion
						transaction = false
						committed = true
					case sql == "ROLLBACK":
						pendingVersion = 0
						transaction = false
						rolledBack = true
					case fail && strings.HasPrefix(sql, "CREATE TABLE IF NOT EXISTS posts"):
						results = append(results, map[string]any{"type": "error", "error": map[string]any{"code": "SQLITE_ERROR", "message": "injected schema failure"}})
						continue
					}
					results = append(results, map[string]any{"type": "ok", "response": map[string]any{"type": "execute", "result": map[string]any{"cols": cols, "rows": rows, "affected_row_count": 0, "last_insert_rowid": nil}}})
				}
				var baton any
				if transaction {
					baton = "transaction"
				}
				json.NewEncoder(w).Encode(map[string]any{"baton": baton, "results": results})
			}))
			defer srv.Close()
			s, err := Open(srv.URL, "test")
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			err = s.Migrate(context.Background())
			if fail {
				if err == nil || committed || !rolledBack || version != 0 {
					t.Fatal(err, committed, rolledBack, version)
				}
			} else if err != nil || !committed || rolledBack || version != 1 {
				t.Fatal(err, committed, rolledBack, version)
			}
		})
	}
}
