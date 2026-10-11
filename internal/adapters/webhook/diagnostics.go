package webhook

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/url"
	"regexp"
	"strings"

	tg "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

var diagnosticURL = regexp.MustCompile(`https?://[^\s<>()]+`)
var youtubeID = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)
var telegramUsername = regexp.MustCompile(`^[A-Za-z0-9_]{1,32}$`)

// Log only identifiers and presence flags, never message text, captions or URLs.
// Called after webhook authentication and target-chat/self-message filtering.
func logUpload(ctx context.Context, updateID int, m *tg.Message, raw json.RawMessage) {
	ids := youtubeIDs(m)
	if m.Video == nil && m.VideoNote == nil && len(ids) == 0 && (m.From == nil || !m.From.IsBot) {
		return
	}
	var extra struct {
		ExternalReply json.RawMessage `json:"external_reply"`
		ForwardOrigin json.RawMessage `json:"forward_origin"`
	}
	_ = json.Unmarshal(raw, &extra)
	attrs := []any{"update_id", updateID, "chat_id", m.Chat.ID, "message_id", m.MessageID, "message_date", m.Date,
		"has_video", m.Video != nil, "has_video_note", m.VideoNote != nil, "has_caption", m.Caption != "",
		"has_document", m.Document != nil, "has_animation", m.Animation != nil, "media_group_id", m.MediaGroupID,
		"youtube_video_ids", ids, "has_reply", m.ReplyToMessage != nil,
		"has_external_reply", present(extra.ExternalReply), "has_forward_origin", present(extra.ForwardOrigin)}
	attrs = appendUser(attrs, "sender", m.From)
	attrs = appendUser(attrs, "via_bot", m.ViaBot)
	if m.SenderChat != nil {
		attrs = append(attrs, "sender_chat_id", m.SenderChat.ID)
	}
	if reply := m.ReplyToMessage; reply != nil {
		attrs = append(attrs, "reply_message_id", reply.MessageID, "reply_youtube_video_ids", youtubeIDs(reply))
		attrs = appendUser(attrs, "reply_sender", reply.From)
		if reply.SenderChat != nil {
			attrs = append(attrs, "reply_sender_chat_id", reply.SenderChat.ID)
		}
	}
	mentions := make([]int64, 0)
	for _, e := range m.CaptionEntities {
		if e.Type == "text_mention" && e.User != nil && len(mentions) < 8 {
			mentions = append(mentions, e.User.ID)
		}
	}
	attrs = append(attrs, "caption_mentioned_user_ids", mentions)
	// Info is intentional: Vercel's default slog level suppresses Debug.
	slog.InfoContext(ctx, "telegram upload diagnostic", attrs...)
}

func present(raw json.RawMessage) bool {
	return len(raw) > 0 && string(raw) != "null"
}

func appendUser(attrs []any, prefix string, u *tg.User) []any {
	if u == nil {
		return attrs
	}
	username := ""
	if telegramUsername.MatchString(u.UserName) {
		username = u.UserName
	}
	return append(attrs, prefix+"_id", u.ID, prefix+"_is_bot", u.IsBot, prefix+"_username", username)
}

func youtubeIDs(m *tg.Message) []string {
	ids := make([]string, 0)
	urls := diagnosticURL.FindAllString(m.Text+" "+m.Caption, -1)
	for _, entities := range [][]tg.MessageEntity{m.Entities, m.CaptionEntities} {
		for _, e := range entities {
			if e.Type == "text_link" {
				urls = append(urls, e.URL)
			}
		}
	}
	for _, value := range urls {
		u, err := url.Parse(value)
		if err != nil {
			continue
		}
		host := strings.ToLower(u.Hostname())
		path := strings.Split(strings.Trim(u.Path, "/"), "/")
		id := ""
		switch host {
		case "youtu.be", "www.youtu.be":
			id = path[0]
		case "youtube.com", "www.youtube.com", "m.youtube.com", "music.youtube.com":
			if path[0] == "watch" {
				id = u.Query().Get("v")
			} else if len(path) > 1 && (path[0] == "shorts" || path[0] == "embed" || path[0] == "live") {
				id = path[1]
			}
		}
		if !youtubeID.MatchString(id) {
			continue
		}
		duplicate := false
		for _, previous := range ids {
			duplicate = duplicate || previous == id
		}
		if !duplicate {
			ids = append(ids, id)
			if len(ids) == 8 {
				break
			}
		}
	}
	return ids
}
