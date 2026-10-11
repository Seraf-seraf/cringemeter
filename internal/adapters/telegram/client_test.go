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
	"time"
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
		if payload.ChatID != -100 || payload.Source != 7 {
			t.Error(payload)
		}
		rows := payload.Markup.InlineKeyboard
		if len(rows) != 3 || len(rows[0]) != 1 || len(rows[1]) != 1 || len(rows[2]) != 1 {
			t.Error(rows)
			return
		}
		if rows[2][0].Text != "👤 Автор: не определён" || rows[2][0].CallbackData == nil || *rows[2][0].CallbackData != "a:9" {
			t.Error(rows)
		}
		cringe, nonCringe := rows[0][0], rows[1][0]
		if cringe.Text != "🤡Кринж · 2" || nonCringe.Text != "🙂‍↕️Некринж · 3" || cringe.CallbackData == nil || nonCringe.CallbackData == nil || *cringe.CallbackData != "v:9:1" || *nonCringe.CallbackData != "v:9:0" {
			t.Error(rows)
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

func TestUserTopEscapesNamesAndShowsAbsoluteScore(t *testing.T) {
	text := TopText([]domain.Rank{{UserID: 7, Score: 100, Name: `Аня <script> & Борис`}, {UserID: 8, Score: 1}})
	for _, want := range []string{"👑 Короли кринжа", `href="tg://user?id=7"`, "Аня &lt;script&gt; &amp; Борис", "🤡 100", "Пользователь 8"} {
		if !strings.Contains(text, want) {
			t.Fatal(text, want)
		}
	}
	if !strings.Contains(TopText(nil), "Пока нет пользователей") {
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

func TestTopCommand(t *testing.T) {
	for _, tc := range []struct {
		text, address string
		ok            bool
	}{
		{"/top", "", true}, {"/top@Cringebot", "Cringebot", true}, {"/topology", "", false}, {"/top@", "", false}, {"hello /top", "", false}, {"/top@a@b", "", false},
	} {
		m := &tg.Message{Text: tc.text, Entities: []tg.MessageEntity{{Type: "bot_command", Offset: 0, Length: len(tc.text)}}}
		address, ok := TopCommand(m)
		if ok != tc.ok || address != tc.address {
			t.Fatal(tc, address, ok)
		}
	}
}

func TestTopStillSendsWhenOptionalNameLookupTimesOut(t *testing.T) {
	sent := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bot123:test/getChatMember":
			select {
			case <-r.Context().Done():
			case <-time.After(2 * time.Second):
			}
		case "/bot123:test/sendMessage":
			var payload struct {
				Text      string `json:"text"`
				ParseMode string `json:"parse_mode"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Error(err)
			}
			if payload.ParseMode != "HTML" {
				t.Error(payload.ParseMode)
			}
			sent <- payload.Text
			w.Write([]byte(`{"ok":true,"result":{}}`))
		default:
			t.Error(r.URL.Path)
		}
	}))
	defer server.Close()
	c := New("123:test")
	c.BaseURL = server.URL
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	if err := c.SendTop(ctx, domain.Group{ID: -100}, []domain.Rank{{UserID: 1, Score: 3}, {UserID: 2, Score: 2}, {UserID: 3, Score: 1}}); err != nil {
		t.Fatal(err)
	}
	select {
	case text := <-sent:
		if !strings.Contains(text, "Пользователь 3") || !strings.Contains(text, "👑 Короли кринжа") {
			t.Fatal(text)
		}
	default:
		t.Fatal("ranking was not sent")
	}
}

func TestSelfVoteWarningIsTelegramAlert(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bot123:test/answerCallbackQuery" {
			t.Error(r.URL.Path)
		}
		var payload struct {
			ID    string `json:"callback_query_id"`
			Text  string `json:"text"`
			Alert bool   `json:"show_alert"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if payload.ID != "self" || payload.Text != "Нельзя голосовать за своё видео" || !payload.Alert {
			t.Error(payload)
		}
		w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	defer server.Close()
	c := New("123:test")
	c.BaseURL = server.URL
	if err := c.Answer(context.Background(), "self", "Нельзя голосовать за своё видео", true); err != nil {
		t.Fatal(err)
	}
}
