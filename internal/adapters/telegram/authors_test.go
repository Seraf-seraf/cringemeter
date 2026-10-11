package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"handler/internal/domain"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tg "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func TestAuthorCallbackIsBoundToPostMessageAndCaller(t *testing.T) {
	for _, data := range []string{"a:7", "a:0", "a:07", "a:-1", "a:7:1", "v:7:1", "a:9999999999999999999999"} {
		q := &tg.CallbackQuery{ID: "c", Data: data, From: &tg.User{ID: 42}, Message: &tg.Message{MessageID: 101, Chat: &tg.Chat{ID: -100}}}
		a, ok := AuthorClick(q)
		if ok != (data == "a:7") || ok && (a.PostID != 7 || a.ChatID != -100 || a.MessageID != 101 || a.UserID != 42 || a.CallbackID != "c") {
			t.Fatal(data, a, ok)
		}
	}
}

func TestAuthorManagementRequiresCurrentAdministratorRole(t *testing.T) {
	for _, tc := range []struct {
		body    string
		allowed bool
		failed  bool
	}{
		{`{"ok":true,"result":{"status":"creator","user":{"id":42}}}`, true, false},
		{`{"ok":true,"result":{"status":"administrator","user":{"id":42}}}`, true, false},
		{`{"ok":true,"result":{"status":"member","user":{"id":42}}}`, false, false},
		{`{"ok":true,"result":{"status":"restricted","user":{"id":42}}}`, false, false},
		{`{"ok":true,"result":{"status":"administrator","user":{"id":99}}}`, false, false},
		{`{"ok":false,"error_code":403}`, false, true},
	} {
		t.Run(tc.body, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/bot123:test/getChatMember" {
					t.Error(r.URL.Path)
				}
				w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			c := New("123:test")
			c.BaseURL = srv.URL
			allowed, err := c.IsAdmin(context.Background(), -100, 42)
			if allowed != tc.allowed || (err != nil) != tc.failed {
				t.Fatal(allowed, err)
			}
		})
	}
}

func TestAuthorSelectionParserBoundsAndRevision(t *testing.T) {
	for _, tc := range []struct {
		data  string
		valid bool
	}{
		{"a:s:7:42:0", true}, {"a:l:7:100000:9", true}, {"a:c:7:9", true},
		{"a:s:7:0:0", false}, {"a:s:7:042:0", false}, {"a:s:7:42:-1", false}, {"a:l:7:100001:0", false}, {"a:c:7:0:1", false},
		{"a:s:9223372036854775807:9223372036854775807:9223372036854775807", true},
		{"a:s:9223372036854775808:42:0", false},
	} {
		q := &tg.CallbackQuery{ID: "c", Data: tc.data, From: &tg.User{ID: 90}, Message: &tg.Message{MessageID: 101, Chat: &tg.Chat{ID: -100}}}
		a, ok := ParseAuthorAction(q)
		if ok != tc.valid || ok && (a.UserID != 90 || a.MessageID != 101 || a.ChatID != -100) {
			t.Fatal(tc, a, ok)
		}
	}
}

func TestAuthorKeyboardShowsUsernameAndKnownParticipantSelection(t *testing.T) {
	var captured tg.InlineKeyboardMarkup
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bot123:test/editMessageReplyMarkup" {
			t.Error(r.URL.Path)
		}
		var body struct {
			Markup tg.InlineKeyboardMarkup `json:"reply_markup"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		captured = body.Markup
		w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	defer srv.Close()
	c := New("123:test")
	c.BaseURL = srv.URL
	p := domain.Post{ID: 7, ChatID: -100, BotMessageID: 101, UploaderID: 42, AuthorUsername: "owner", AuthorRevision: 9}
	if err := c.ChooseAuthor(context.Background(), p, domain.Counts{Cringe: 2, NonCringe: 1}, domain.ParticipantPage{Users: []domain.Participant{{ID: 43, Username: "new_owner"}}, Index: 1, Pages: 3}); err != nil {
		t.Fatal(err)
	}
	rows := captured.InlineKeyboard
	if len(rows) != 6 || rows[2][0].Text != "👤 Автор: @owner" || rows[3][0].Text != "@new_owner" || *rows[3][0].CallbackData != "a:s:7:43:9" || *rows[4][0].CallbackData != "a:l:7:0:9" || rows[4][1].Text != "2/3" || *rows[4][1].CallbackData != "a:l:7:1:9" || *rows[4][2].CallbackData != "a:l:7:2:9" || *rows[5][0].CallbackData != "a:c:7:9" {
		t.Fatal(rows)
	}
	if err := c.Edit(context.Background(), p, domain.Counts{Cringe: 3}); err != nil {
		t.Fatal(err)
	}
	if len(captured.InlineKeyboard) != 3 || captured.InlineKeyboard[2][0].Text != "👤 Автор: @owner" {
		t.Fatal(captured)
	}
}

func TestSelectedAuthorMustBeCurrentHumanMember(t *testing.T) {
	for _, body := range []string{
		`{"ok":true,"result":{"status":"member","user":{"id":42,"username":"person"}}}`,
		`{"ok":true,"result":{"status":"member","user":{"id":42,"is_bot":true}}}`,
		`{"ok":true,"result":{"status":"left","user":{"id":42}}}`,
		`{"ok":true,"result":{"status":"restricted","is_member":false,"user":{"id":42}}}`,
		`{"ok":true,"result":{"status":"member","user":{"id":99}}}`,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
		c := New("123:test")
		c.BaseURL = srv.URL
		u, err := c.LookupParticipant(context.Background(), -100, 42)
		srv.Close()
		allowed := strings.Contains(body, `"username":"person"`)
		if (err == nil) != allowed || allowed && u.Username != "person" {
			t.Fatal(u, err)
		}
	}
}

func TestAuthorPaginationAlwaysHasThreeNavigationButtonsAtEdges(t *testing.T) {
	for _, index := range []int{0, 2} {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Markup tg.InlineKeyboardMarkup `json:"reply_markup"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				rows := body.Markup.InlineKeyboard
				nav := rows[len(rows)-2]
				previous, next := index, index
				if index > 0 {
					previous--
				}
				if index < 2 {
					next++
				}
				if len(nav) != 3 || nav[0].Text != "←" || nav[1].Text != fmt.Sprintf("%d/3", index+1) || nav[2].Text != "→" || *nav[0].CallbackData != fmt.Sprintf("a:l:7:%d:9", previous) || *nav[1].CallbackData != fmt.Sprintf("a:l:7:%d:9", index) || *nav[2].CallbackData != fmt.Sprintf("a:l:7:%d:9", next) {
					t.Error(nav)
				}
				w.Write([]byte(`{"ok":true,"result":true}`))
			}))
			defer srv.Close()
			c := New("123:test")
			c.BaseURL = srv.URL
			if err := c.ChooseAuthor(context.Background(), domain.Post{ID: 7, ChatID: -100, AuthorRevision: 9}, domain.Counts{}, domain.ParticipantPage{Index: index, Pages: 3}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
