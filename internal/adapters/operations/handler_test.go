package operations

import (
	"context"
	"database/sql"
	"handler/internal/adapters/turso"
	_ "modernc.org/sqlite"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdministrativeBoundary(t *testing.T) {
	for _, tc := range []struct {
		method, secret, body string
		status               int
		opens                bool
	}{
		{"GET", "s", `{"action":"probe"}`, 405, false},
		{"POST", "", `{"action":"migrate"}`, 401, false},
		{"POST", "wrong", `{"action":"migrate"}`, 401, false},
		{"POST", "s", `{"action":"drop"}`, 400, false},
		{"POST", "s", `{"action":"probe","query":"DROP TABLE media"}`, 400, false},
		{"POST", "s", `{"action":"probe"}{}`, 400, false},
		{"POST", "s", `{"action":"probe"}`, 200, true},
		{"POST", "s", `{"action":"migrate"}`, 200, true},
	} {
		t.Run(tc.method+tc.secret+tc.body, func(t *testing.T) {
			opened := false
			h := Handler{Secret: "s", Open: func() (*turso.Store, error) {
				opened = true
				db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "admin.db"))
				return &turso.Store{DB: db}, err
			}}
			r := httptest.NewRequest(tc.method, "/api/operations", strings.NewReader(tc.body))
			r.Header.Set("X-Cringemeter-Admin-Secret", tc.secret)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status || opened != tc.opens {
				t.Fatal(w.Code, opened)
			}
		})
	}
}

func TestCheckRequiresMigration(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "check.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := &turso.Store{DB: db}
	if err := s.Check(context.Background()); err == nil {
		t.Fatal("unmigrated database accepted")
	}
}
