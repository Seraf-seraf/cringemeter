package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	tg "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"handler/internal/config"
)

func TestUploadDiagnosticsRespectAuthenticationChatAndOptIn(t *testing.T) {
	body := `{"update_id":17,"message":{"message_id":2,"chat":{"id":-100,"type":"supergroup"},"from":{"id":42,"is_bot":true,"username":"downloader"},"video":{"file_unique_id":"v"}}}`
	for _, tc := range []struct {
		name, secret, body string
		enabled, logged    bool
	}{
		{"enabled", "s", body, true, true},
		{"disabled", "s", body, false, false},
		{"unauthenticated", "wrong", body, true, false},
		{"foreign group", "s", strings.Replace(body, "-100", "-200", 1), true, false},
		{"own bot", "s", strings.Replace(body, `"id":42`, `"id":123`, 1), true, false},
		{"ordinary text", "s", strings.Replace(strings.Replace(body, `"video":{"file_unique_id":"v"}`, `"text":"private message"`, 1), `"is_bot":true`, `"is_bot":false`, 1), true, false},
		{"bot progress reply", "s", strings.Replace(body, `"video":{"file_unique_id":"v"}`, `"text":"private progress message"`, 1), true, true},
		{"bot document", "s", strings.Replace(body, `"video":{"file_unique_id":"v"}`, `"document":{"file_unique_id":"v"}`, 1), true, true},
		{"YouTube request", "s", strings.Replace(body, `"video":{"file_unique_id":"v"}`, `"text":"https://youtu.be/abcdefghijk?token=private"`, 1), true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
			defer slog.SetDefault(previous)
			h := Handler{Config: config.Config{WebhookSecret: "s", ChatID: -100, BotID: 123, DebugUploads: tc.enabled}, Process: func(context.Context, tg.Update) error { return nil }}
			r := httptest.NewRequest("POST", "/api/webhook", strings.NewReader(tc.body))
			r.Header.Set("X-Telegram-Bot-Api-Secret-Token", tc.secret)
			h.ServeHTTP(httptest.NewRecorder(), r)
			if (output.Len() > 0) != tc.logged || strings.Contains(output.String(), "private") {
				t.Fatal("diagnostic boundary or privacy failed", output.String())
			}
		})
	}
}

func TestUploadDiagnosticCapturesReplyWithoutMessageContent(t *testing.T) {
	body := `{"update_id":18,"message":{"message_id":3,"date":1234,"chat":{"id":-100,"type":"supergroup"},"from":{"id":99,"is_bot":true,"username":"SaveYoutubeBot","first_name":"private-name"},"video":{"file_unique_id":"private-file","file_name":"abcdefghijk.mp4"},"guest_bot_caller_user":{"id":42,"first_name":"private-name"},"guest_bot_caller_chat":{"id":-100},"reply_markup":{"inline_keyboard":[[{"text":"private-button","url":"https://youtu.be/ZYXWVUTSRQP?token=private-token"}]]},"caption":"private-caption https://youtube.com/watch?v=abcdefghijk&token=private-token","caption_entities":[{"type":"text_mention","offset":0,"length":1,"user":{"id":42,"first_name":"private-name"}}],"reply_to_message":{"message_id":2,"from":{"id":42,"username":"requester"},"text":"private-request https://youtube.com/shorts/abcdefghijk?secret=private-secret"},"external_reply":{"origin":{"type":"hidden_user","sender_user_name":"private-name"}},"forward_origin":{"type":"hidden_user","sender_user_name":"private-name"},"via_bot":{"id":77,"is_bot":true,"username":"inline_bot"}}}`
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	defer slog.SetDefault(previous)
	h := Handler{Config: config.Config{WebhookSecret: "s", ChatID: -100, BotID: 123, DebugUploads: true}, Process: func(context.Context, tg.Update) error { return nil }}
	r := httptest.NewRequest("POST", "/api/webhook", strings.NewReader(body))
	r.Header.Set("X-Telegram-Bot-Api-Secret-Token", "s")
	h.ServeHTTP(httptest.NewRecorder(), r)
	var logged map[string]any
	if err := json.Unmarshal(output.Bytes(), &logged); err != nil {
		t.Fatal(err)
	}
	for field, want := range map[string]any{"sender_id": float64(99), "sender_username": "SaveYoutubeBot", "sender_is_bot": true, "reply_message_id": float64(2), "reply_sender_id": float64(42), "has_reply": true, "has_external_reply": true, "has_forward_origin": true, "via_bot_id": float64(77)} {
		if logged[field] != want {
			t.Fatal(field, logged[field], want)
		}
	}
	if strings.Contains(output.String(), "private") || strings.Contains(output.String(), "https://") {
		t.Fatal("message content leaked", output.String())
	}
	if logged["guest_caller_id"] != float64(42) || logged["guest_caller_chat_id"] != float64(-100) || logged["filename_youtube_id"] != "abcdefghijk" || logged["inline_button_count"] != float64(1) {
		t.Fatal("missing correlation fields", logged)
	}
	for _, field := range []string{"youtube_video_ids", "reply_youtube_video_ids"} {
		values, ok := logged[field].([]any)
		if !ok || len(values) < 1 || values[0] != "abcdefghijk" {
			t.Fatal(field, logged[field])
		}
	}
}
