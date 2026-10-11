package operations

import (
	"context"
	"encoding/json"
	"handler/internal/adapters/httpauth"
	"handler/internal/adapters/httpjson"
	"handler/internal/adapters/turso"
	"log/slog"
	"net/http"
	"time"
)

const RequestTimeout = 30 * time.Second

type Handler struct {
	Secret          string
	ChatID          int64
	DownloaderBotID int64
	Open            func() (*turso.Store, error)
}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Path != "/api/operations" {
		http.NotFound(w, r)
		return
	}
	if r.Method != "POST" {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", 405)
		return
	}
	if !httpauth.Matches(h.Secret, r.Header.Get("X-Cringemeter-Admin-Secret")) {
		http.Error(w, "unauthorized", 401)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	defer r.Body.Close()
	var input struct {
		Action string `json:"action"`
	}
	if err := httpjson.DecodeObject(r.Body, &input); err != nil || (input.Action != "probe" && input.Action != "check" && input.Action != "migrate" && input.Action != "remove_self_votes" && input.Action != "diagnose_uploads" && input.Action != "register_downloader") {
		http.Error(w, "invalid action", 400)
		return
	}
	if input.Action == "diagnose_uploads" && h.ChatID >= 0 {
		http.Error(w, "invalid diagnostic chat", 400)
		return
	}

	if input.Action == "register_downloader" && h.DownloaderBotID <= 0 {
		http.Error(w, "invalid downloader", 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), RequestTimeout)
	defer cancel()
	s, err := h.Open()
	if err != nil {
		failure(ctx, w, input.Action)
		return
	}
	defer s.Close()
	report := map[string]any{"ok": true, "action": input.Action}
	switch input.Action {
	case "diagnose_uploads":
		report["uploads"], err = s.RecentUploads(ctx, h.ChatID)
		if err == nil {
			report["pending_requests"], err = s.PendingRequests(ctx, h.ChatID)
		}
	case "register_downloader":
		err = s.RegisterDownloader(ctx, h.DownloaderBotID)
	case "migrate":
		err = s.Migrate(ctx)
	case "check":
		err = s.Check(ctx)
	case "remove_self_votes":
		var removed int64
		removed, err = s.RemoveSelfVotes(ctx)
		report["removed"] = removed
	case "probe":
		var one int
		err = s.DB.QueryRowContext(ctx, "SELECT 1").Scan(&one)
		if err == nil {
			var enabled int
			err = s.DB.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&enabled)
			report["foreign_keys_enabled"] = enabled == 1
		}
	}
	if err != nil {
		failure(ctx, w, input.Action)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(report)
}
func failure(ctx context.Context, w http.ResponseWriter, phase string) {
	slog.ErrorContext(ctx, "database operation failed", "phase", phase, "code", "database_failed")
	http.Error(w, "temporarily unavailable", 503)
}
