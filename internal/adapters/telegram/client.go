package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
		return &domain.APIError{Code: code, RetryAfter: reply.Parameters.RetryAfter, Confirmed: code >= 400 && code < 500, MessageGone: code == 400 && strings.Contains(lower, "message to delete not found"), NotModified: code == 400 && strings.Contains(lower, "message is not modified")}
	}
	if result != nil && json.Unmarshal(reply.Result, result) != nil {
		return &domain.APIError{}
	}
	return nil
}

func Keyboard(postID int64, counts domain.Counts) tg.InlineKeyboardMarkup {
	id := strconv.FormatInt(postID, 10)
	return tg.NewInlineKeyboardMarkup(tg.NewInlineKeyboardRow(
		tg.NewInlineKeyboardButtonData("🤡Кринж · "+strconv.FormatInt(counts.Cringe, 10), "v:"+id+":1"),
		tg.NewInlineKeyboardButtonData("Некринж🙂‍↕️ · "+strconv.FormatInt(counts.NonCringe, 10), "v:"+id+":0"),
	))
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
	if m.From != nil {
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
