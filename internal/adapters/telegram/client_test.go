package telegram

import (
	"context"
	"encoding/json"
	"errors"
	tg "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"handler/internal/domain"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCopySendsMessageReferenceAndKeyboard(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/bot123:test/copyMessage" {
			t.Error(r.Method, r.URL)
		}
		var payload struct {
			ChatID int64                   `json:"chat_id"`
			Source int                     `json:"message_id"`
			Markup tg.InlineKeyboardMarkup `json:"reply_markup"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if payload.ChatID != -100 || payload.Source != 7 || len(payload.Markup.InlineKeyboard) != 1 {
			t.Error(payload)
		}
		buttons := payload.Markup.InlineKeyboard[0]
		if buttons[0].Text != "🤡Кринж · 2" || buttons[1].Text != "Некринж🙂‍↕️ · 3" || *buttons[0].CallbackData != "v:9:1" {
			t.Error(buttons)
		}
		w.Write([]byte(`{"ok":true,"result":{"message_id":101}}`))
	}))
	defer server.Close()
	c := New("123:test")
	c.BaseURL = server.URL
	id, err := c.Copy(context.Background(), domain.Post{ID: 9, ChatID: -100, SourceMessageID: 7}, domain.Counts{Cringe: 2, NonCringe: 3})
	if err != nil || id != 101 {
		t.Fatal(id, err)
	}
}

func TestAPIErrorsAreSafeAndClassified(t *testing.T) {
	for _, tc := range []struct {
		body                       string
		confirmed, gone, unchanged bool
		retry                      int
	}{
		{`{"ok":false,"error_code":400,"description":"Bad Request: message to delete not found"}`, true, true, false, 0},
		{`{"ok":false,"error_code":400,"description":"Bad Request: message is not modified"}`, true, false, true, 0},
		{`{"ok":false,"error_code":429,"description":"token:private","parameters":{"retry_after":7}}`, true, false, false, 7},
		{`{"ok":false,"error_code":500,"description":"token:private"}`, false, false, false, 0},
		{`{"ok":true,"result":{}}`, false, false, false, 0},
		{`not JSON`, false, false, false, 0},
	} {
		t.Run(tc.body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(tc.body)) }))
			defer server.Close()
			c := New("123:test")
			c.BaseURL = server.URL
			_, err := c.Copy(context.Background(), domain.Post{}, domain.Counts{})
			var api *domain.APIError
			if !errors.As(err, &api) || api.Confirmed != tc.confirmed || api.MessageGone != tc.gone || api.NotModified != tc.unchanged || api.RetryAfter != tc.retry || strings.Contains(err.Error(), "private") {
				t.Fatal(err, api)
			}
		})
	}
}

func TestMediaMapping(t *testing.T) {
	for _, tc := range []struct {
		name, body, kind string
		ok               bool
	}{
		{"video", `{"message_id":1,"chat":{"id":-100},"from":{"id":42},"video":{"file_unique_id":"v"}}`, "video", true},
		{"note", `{"message_id":1,"chat":{"id":-100},"video_note":{"file_unique_id":"v"}}`, "video_note", true},
		{"forward", `{"message_id":1,"chat":{"id":-100},"from":{"id":42},"forward_sender_name":"hidden","video":{"file_unique_id":"v"}}`, "video", true},
		{"other bot", `{"message_id":1,"chat":{"id":-100},"from":{"id":99,"is_bot":true},"video":{"file_unique_id":"v"}}`, "video", true},
		{"document", `{"message_id":1,"chat":{"id":-100},"document":{"mime_type":"video/mp4","file_unique_id":"v"}}`, "", false},
		{"animation", `{"message_id":1,"chat":{"id":-100},"animation":{"file_unique_id":"v"}}`, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var m tg.Message
			if err := json.Unmarshal([]byte(tc.body), &m); err != nil {
				t.Fatal(err)
			}
			in, ok := Incoming(&m)
			if ok != tc.ok || in.MediaType != tc.kind {
				t.Fatal(in, ok)
			}
			if ok && m.From != nil && *in.UploaderID != m.From.ID {
				t.Fatal(in)
			}
		})
	}
}

func TestMessageLinksAndTop(t *testing.T) {
	for _, tc := range []struct {
		g    domain.Group
		id   int
		link string
	}{
		{domain.Group{ID: -1001234, Type: "supergroup", Username: "testgroup"}, 5, "https://t.me/testgroup/5"},
		{domain.Group{ID: -1001234, Type: "supergroup"}, 5, "https://t.me/c/1234/5"},
		{domain.Group{ID: -1234, Type: "group"}, 5, ""},
		{domain.Group{Username: `bad"<script>`}, 5, ""},
		{domain.Group{Username: "testgroup"}, 0, ""},
	} {
		if got := MessageLink(tc.g, tc.id); got != tc.link {
			t.Fatal(got, tc.link)
		}
	}
	g := domain.Group{ID: -1001234, Type: "supergroup"}
	text := TopText(g, []domain.Rank{{MediaID: 7, MessageID: 5, Counts: domain.Counts{Cringe: 3, NonCringe: 2}}, {MediaID: 8, Counts: domain.Counts{Cringe: 1, NonCringe: 4}}})
	if !strings.Contains(text, "60.0% (3/5)") || !strings.Contains(text, `href="https://t.me/c/1234/5"`) || !strings.Contains(text, "ссылка недоступна") {
		t.Fatal(text)
	}
	if !strings.Contains(TopText(g, nil), "Пока нет") {
		t.Fatal("empty top")
	}
}

func TestCallbackContract(t *testing.T) {
	for _, data := range []string{"v:1:1", "v:9223372036854775807:0", "v:0:1", "v:-1:1", "v:01:1", "v:1:2", "1:1", "v:999999999999999999999:1"} {
		q := &tg.CallbackQuery{ID: "c", Data: data, From: &tg.User{ID: 42}, Message: &tg.Message{MessageID: 1, Chat: &tg.Chat{ID: -100}}}
		b, ok := Ballot(q)
		want := data == "v:1:1" || data == "v:9223372036854775807:0"
		if ok != want || ok && b.UserID != 42 {
			t.Fatal(data, b, ok)
		}
	}
}
