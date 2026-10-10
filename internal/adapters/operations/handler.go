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
	Secret string
	Open   func() (*turso.Store, error)
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
	if err := httpjson.DecodeObject(r.Body, &input); err != nil || (input.Action != "probe" && input.Action != "check" && input.Action != "migrate") {
		http.Error(w, "invalid action", 400)
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
	switch input.Action {
	case "migrate":
		err = s.Migrate(ctx)
	case "check":
		err = s.Check(ctx)
	case "probe":
		var one int
		err = s.DB.QueryRowContext(ctx, "SELECT 1").Scan(&one)
	}
	if err != nil {
		failure(ctx, w, input.Action)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "action": input.Action})
}
func failure(ctx context.Context, w http.ResponseWriter, phase string) {
	slog.ErrorContext(ctx, "database operation failed", "phase", phase, "code", "database_failed")
	http.Error(w, "temporarily unavailable", 503)
}
