package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	tg "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"handler/internal/domain"
)

const APITimeout = 3 * time.Second
const MaxResponseBytes = 1 << 20
const TopNamesTimeout = time.Second

type Client struct {
	Token   string
	BaseURL string
	HTTP    *http.Client
}

func New(token string) *Client {
	return &Client{Token: token, BaseURL: "https://api.telegram.org", HTTP: &http.Client{Timeout: APITimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (c *Client) Call(ctx context.Context, method string, payload any, result any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return errors.New("invalid API payload")
	}
	ctx, cancel := context.WithTimeout(ctx, APITimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/bot"+c.Token+"/"+method, bytes.NewReader(data))
	if err != nil {
		return &domain.APIError{}
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.HTTP.Do(req)
	if err != nil {
		return &domain.APIError{}
	}
	defer res.Body.Close()
	data, err = io.ReadAll(io.LimitReader(res.Body, MaxResponseBytes+1))
	if err != nil || len(data) > MaxResponseBytes {
		return &domain.APIError{}
	}
	var reply struct {
		OK          bool            `json:"ok"`
		Code        int             `json:"error_code"`
		Description string          `json:"description"`
		Result      json.RawMessage `json:"result"`
		Parameters  struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if json.Unmarshal(data, &reply) != nil {
		return &domain.APIError{}
	}
	if !reply.OK || res.StatusCode != 200 {
		code := reply.Code
		if code == 0 {
			code = res.StatusCode
		}
		lower := strings.ToLower(reply.Description)
		return &domain.APIError{Code: code, RetryAfter: reply.Parameters.RetryAfter, Confirmed: code >= 400 && code < 500, MessageGone: code == 400 && (strings.Contains(lower, "message to delete not found") || strings.Contains(lower, "message to edit not found")), NotModified: code == 400 && strings.Contains(lower, "message is not modified")}
	}
	if result != nil && json.Unmarshal(reply.Result, result) != nil {
		return &domain.APIError{}
	}
	return nil
}

func Keyboard(postID int64, counts domain.Counts) tg.InlineKeyboardMarkup {
	id := strconv.FormatInt(postID, 10)
	return tg.NewInlineKeyboardMarkup(
		tg.NewInlineKeyboardRow(tg.NewInlineKeyboardButtonData("🤡Кринж · "+strconv.FormatInt(counts.Cringe, 10), "v:"+id+":1")),
		tg.NewInlineKeyboardRow(tg.NewInlineKeyboardButtonData("🙂‍↕️Некринж · "+strconv.FormatInt(counts.NonCringe, 10), "v:"+id+":0")),
	)
}

func (c *Client) Copy(ctx context.Context, p domain.Post, counts domain.Counts) (int, error) {
	var result struct {
		MessageID int `json:"message_id"`
	}
	err := c.Call(ctx, "copyMessage", map[string]any{"chat_id": p.ChatID, "from_chat_id": p.ChatID, "message_id": p.SourceMessageID, "reply_markup": Keyboard(p.ID, counts)}, &result)
	if err == nil && result.MessageID <= 0 {
		return 0, &domain.APIError{}
	}
	return result.MessageID, err
}

func (c *Client) Delete(ctx context.Context, chatID int64, messageID int) error {
	var ok bool
	err := c.Call(ctx, "deleteMessage", map[string]any{"chat_id": chatID, "message_id": messageID}, &ok)
	if err == nil && !ok {
		return &domain.APIError{}
	}
	return err
}

func Incoming(m *tg.Message) (domain.Incoming, bool) {
	if m == nil || m.Chat == nil || m.MessageID <= 0 {
		return domain.Incoming{}, false
	}
	in := domain.Incoming{ChatID: m.Chat.ID, MessageID: m.MessageID}
	if m.From != nil && m.From.ID > 0 && m.SenderChat == nil {
		userID := m.From.ID
		in.UploaderID = &userID
	}
	switch {
	case m.Video != nil && m.VideoNote == nil:
		in.MediaType, in.UniqueID = "video", m.Video.FileUniqueID
	case m.VideoNote != nil && m.Video == nil:
		in.MediaType, in.UniqueID = "video_note", m.VideoNote.FileUniqueID
	default:
		return domain.Incoming{}, false
	}
	return in, in.UniqueID != ""
}

func (c *Client) Answer(ctx context.Context, id, text string) error {
	return c.Call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": id, "text": text}, nil)
}

func (c *Client) Edit(ctx context.Context, p domain.Post, counts domain.Counts) error {
	return c.Call(ctx, "editMessageReplyMarkup", map[string]any{"chat_id": p.ChatID, "message_id": p.BotMessageID, "reply_markup": Keyboard(p.ID, counts)}, nil)
}

func Ballot(q *tg.CallbackQuery) (domain.Ballot, bool) {
	if q == nil || q.Message == nil || q.Message.Chat == nil || q.From == nil || q.ID == "" || len(q.Data) > 64 {
		return domain.Ballot{}, false
	}
	parts := strings.Split(q.Data, ":")
	if len(parts) != 3 || parts[0] != "v" || (parts[2] != "0" && parts[2] != "1") {
		return domain.Ballot{}, false
	}
	postID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || postID <= 0 || strconv.FormatInt(postID, 10) != parts[1] {
		return domain.Ballot{}, false
	}
	vote := 0
	if parts[2] == "1" {
		vote = 1
	}
	return domain.Ballot{CallbackID: q.ID, PostID: postID, ChatID: q.Message.Chat.ID, MessageID: q.Message.MessageID, UserID: q.From.ID, Vote: vote}, true
}

func TopText(ranks []domain.Rank) string {
	if len(ranks) == 0 {
		return "👑 Короли кринжа\nПока нет голосов «Кринж» за видео пользователей."
	}
	var text strings.Builder
	text.WriteString("<b>👑 Короли кринжа</b>\n")
	for i, r := range ranks {
		name := r.Name
		if name == "" {
			name = "Пользователь " + strconv.FormatInt(r.UserID, 10)
		}
		fmt.Fprintf(&text, "%d. <a href=\"tg://user?id=%d\">%s</a> — 🤡 %d\n", i+1, r.UserID, html.EscapeString(name), r.Score)
	}
	return text.String()
}

func (c *Client) SendTop(ctx context.Context, group domain.Group, ranks []domain.Rank) error {
	// Names are optional: reserve the webhook budget for sending the ranking.
	namesCtx, cancel := context.WithTimeout(ctx, TopNamesTimeout)
	defer cancel()
	ranks = append([]domain.Rank(nil), ranks...)
	for i := range ranks {
		if namesCtx.Err() != nil {
			break
		}
		var member struct {
			User tg.User `json:"user"`
		}
		if err := c.Call(namesCtx, "getChatMember", map[string]any{"chat_id": group.ID, "user_id": ranks[i].UserID}, &member); err == nil {
			ranks[i].Name = strings.TrimSpace(member.User.FirstName + " " + member.User.LastName)
			if ranks[i].Name == "" {
				ranks[i].Name = member.User.UserName
			}
		}
	}
	return c.Call(ctx, "sendMessage", map[string]any{"chat_id": group.ID, "text": TopText(ranks), "parse_mode": "HTML", "link_preview_options": map[string]bool{"is_disabled": true}}, nil)
}

func TopCommand(m *tg.Message) (string, bool) {
	if m == nil || !m.IsCommand() || !strings.HasPrefix(m.Text, "/") {
		return "", false
	}
	fields := strings.Fields(m.Text)
	if len(fields) == 0 {
		return "", false
	}
	command := strings.Split(fields[0], "@")
	if len(command) > 2 || command[0] != "/top" {
		return "", false
	}
	if len(command) == 2 {
		if command[1] == "" {
			return "", false
		}
		return command[1], true
	}
	return "", true
}

func (c *Client) Username(ctx context.Context) (string, error) {
	var user tg.User
	if err := c.Call(ctx, "getMe", map[string]any{}, &user); err != nil {
		return "", err
	}
	return user.UserName, nil
}
