package webhook

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	tg "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"handler/internal/config"
)

func TestHTTPBoundary(t *testing.T) {
	message := `{"update_id":1,"message":{"message_id":2,"chat":{"id":-100,"type":"supergroup"},"from":{"id":42},"video":{"file_unique_id":"v"}}}`
	callback := `{"update_id":2,"callback_query":{"id":"c","from":{"id":42},"data":"v:1:1","message":{"message_id":3,"chat":{"id":-100,"type":"supergroup"},"from":{"id":123}}}}`
	for _, tc := range []struct {
		name, method, secret, body string
		status, calls              int
	}{
		{"method", "GET", "s", message, 405, 0}, {"missing secret", "POST", "", message, 401, 0}, {"wrong secret", "POST", "x", message, 401, 0},
		{"message", "POST", "s", message, 200, 1}, {"callback", "POST", "s", callback, 200, 1},
		{"foreign chat", "POST", "s", strings.Replace(message, "-100", "-200", 1), 200, 0}, {"foreign callback", "POST", "s", strings.Replace(callback, "-100", "-200", 1), 200, 0},
		{"self", "POST", "s", strings.Replace(message, `"id":42`, `"id":123`, 1), 200, 0}, {"foreign bot", "POST", "s", strings.Replace(callback, `"id":123`, `"id":999`, 1), 200, 0},
		{"private", "POST", "s", strings.Replace(message, "supergroup", "private", 1), 200, 0}, {"trailing", "POST", "s", message + `{}`, 400, 0},
		{"null", "POST", "s", `null`, 400, 0}, {"invalid", "POST", "s", `{"update_id":1}`, 400, 0}, {"missing chat", "POST", "s", `{"update_id":1,"message":{"message_id":1}}`, 400, 0},
		{"oversize", "POST", "s", strings.Repeat(" ", MaxBodyBytes+1), 413, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			h := Handler{Config: config.Config{WebhookSecret: "s", ChatID: -100, BotID: 123}, Process: func(ctx context.Context, u tg.Update) error {
				calls++
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("deadline missing")
				}
				return nil
			}}
			r := httptest.NewRequest(tc.method, "/api/webhook", strings.NewReader(tc.body))
			r.Header.Set("X-Telegram-Bot-Api-Secret-Token", tc.secret)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status || calls != tc.calls {
				t.Fatalf("status=%d calls=%d, want %d/%d", w.Code, calls, tc.status, tc.calls)
			}
		})
	}
}

func TestProcessingErrorIsOpaque(t *testing.T) {
	h := Handler{Config: config.Config{WebhookSecret: "s", ChatID: -100}, Process: func(context.Context, tg.Update) error { return errors.New("private database credentials") }}
	r := httptest.NewRequest("POST", "/api/webhook", strings.NewReader(`{"update_id":1,"message":{"message_id":2,"chat":{"id":-100,"type":"group"}}}`))
	r.Header.Set("X-Telegram-Bot-Api-Secret-Token", "s")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 503 || strings.Contains(w.Body.String(), "credentials") {
		t.Fatal(w)
	}
}

func TestDuplicateKeysAndOversizedTrailingData(t *testing.T) {
	for _, tc := range []struct {
		body string
		code int
	}{
		{`{"update_id":1,"update_id":2,"message":{"message_id":1,"chat":{"id":-100,"type":"group"}}}`, 400},
		{`{"update_id":1,"message":{"message_id":1,"chat":{"id":-100,"type":"group"}}}` + strings.Repeat(" ", MaxBodyBytes), 413},
	} {
		called := false
		h := Handler{Config: config.Config{WebhookSecret: "s", ChatID: -100}, Process: func(context.Context, tg.Update) error { called = true; return nil }}
		r := httptest.NewRequest("POST", "/api/webhook", strings.NewReader(tc.body))
		r.Header.Set("X-Telegram-Bot-Api-Secret-Token", "s")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.code || called {
			t.Fatal(w.Code, called)
		}
	}
}
