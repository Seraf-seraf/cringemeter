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
		{"POST", "", `{"action":"register_downloader"}`, 401, false},
		{"POST", "s", `{"action":"register_downloader"}`, 400, false},
		{"POST", "s", `{"action":"register_downloader","bot_id":99}`, 400, false},
		{"POST", "", `{"action":"migrate"}`, 401, false},
		{"POST", "wrong", `{"action":"migrate"}`, 401, false},
		{"POST", "", `{"action":"remove_self_votes"}`, 401, false},
		{"POST", "wrong", `{"action":"remove_self_votes"}`, 401, false},
		{"POST", "", `{"action":"diagnose_uploads"}`, 401, false},
		{"POST", "wrong", `{"action":"diagnose_uploads"}`, 401, false},
		{"POST", "s", `{"action":"diagnose_uploads","chat_id":-200}`, 400, false},
		{"POST", "s", `{"action":"diagnose_uploads","query":"SELECT * FROM votes"}`, 400, false},
		{"POST", "s", `{"action":"diagnose_uploads"}`, 400, false},
		{"POST", "s", `{"action":"remove_self_votes","query":"DELETE FROM author_votes"}`, 400, false},
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

func TestUploadDiagnosticsReturnFixedReadOnlyChatReport(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "diagnostic.db"))
	if err != nil {
		t.Fatal(err)
	}
	s := &turso.Store{DB: db}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO media(id,file_unique_id) VALUES(1,'v'); INSERT INTO posts(media_id,chat_id,source_message_id,uploader_user_id,media_type) VALUES(1,-100,7,42,'video'),(1,-200,8,43,'video')`); err != nil {
		t.Fatal(err)
	}
	h := Handler{Secret: "s", ChatID: -100, Open: func() (*turso.Store, error) { return s, nil }}
	r := httptest.NewRequest("POST", "/api/operations", strings.NewReader(`{"action":"diagnose_uploads"}`))
	r.Header.Set("X-Cringemeter-Admin-Secret", "s")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"source_message_id":7`) || strings.Contains(w.Body.String(), `"source_message_id":8`) {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestRegisterDownloaderUsesConfiguredID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "downloader.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	s := &turso.Store{DB: db}
	if err = s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	h := Handler{Secret: "s", DownloaderBotID: 99, Open: func() (*turso.Store, error) { return s, nil }}
	r := httptest.NewRequest("POST", "/api/operations", strings.NewReader(`{"action":"register_downloader"}`))
	r.Header.Set("X-Cringemeter-Admin-Secret", "s")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	// Handler closes its DB; reopen the same file to verify the persisted ID.
	db, err = sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var id int64
	if err = db.QueryRow(`SELECT user_id FROM bot_senders`).Scan(&id); err != nil || id != 99 {
		t.Fatal(id, err)
	}
}

func TestExplicitSelfVoteCleanupRequiresCurrentSchemaAndReportsCount(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "cleanup.db"))
	if err != nil {
		t.Fatal(err)
	}
	store := &turso.Store{DB: db}
	if err = store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	h := Handler{Secret: "s", Open: func() (*turso.Store, error) { return store, nil }}
	request := httptest.NewRequest("POST", "/api/operations", strings.NewReader(`{"action":"remove_self_votes"}`))
	request.Header.Set("X-Cringemeter-Admin-Secret", "s")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, request)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"removed":0`) {
		t.Fatal(w.Code, w.Body.String())
	}
}
