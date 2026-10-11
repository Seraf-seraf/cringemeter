package webhook

import (
	"context"
	"encoding/json"
	"log/slog"
	"regexp"

	tg "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"handler/internal/adapters/telegram"
)

var telegramUsername = regexp.MustCompile(`^[A-Za-z0-9_]{1,32}$`)

// Log only identifiers and presence flags, never message text, captions or URLs.
// Called after webhook authentication and target-chat/self-message filtering.
func logUpload(ctx context.Context, updateID int, m *tg.Message, raw json.RawMessage) {
	ids := youtubeIDs(m)
	sources := telegram.SourceIDs(m)
	if m.Video == nil && m.VideoNote == nil && len(sources) == 0 && (m.From == nil || !m.From.IsBot) {
		return
	}
	var extra struct {
		ExternalReply json.RawMessage `json:"external_reply"`
		ForwardOrigin json.RawMessage `json:"forward_origin"`
		GuestCaller   *tg.User        `json:"guest_bot_caller_user"`
		GuestChat     *tg.Chat        `json:"guest_bot_caller_chat"`
	}
	_ = json.Unmarshal(raw, &extra)
	attrs := []any{"update_id", updateID, "chat_id", m.Chat.ID, "message_id", m.MessageID, "message_date", m.Date,
		"has_video", m.Video != nil, "has_video_note", m.VideoNote != nil, "has_caption", m.Caption != "",
		"has_document", m.Document != nil, "has_animation", m.Animation != nil, "media_group_id", m.MediaGroupID,
		"youtube_video_ids", ids, "source_video_ids", sources, "has_reply", m.ReplyToMessage != nil,
		"has_external_reply", present(extra.ExternalReply), "has_forward_origin", present(extra.ForwardOrigin)}
	attrs = appendUser(attrs, "sender", m.From)
	attrs = appendUser(attrs, "via_bot", m.ViaBot)
	attrs = appendUser(attrs, "guest_caller", extra.GuestCaller)
	if extra.GuestChat != nil {
		attrs = append(attrs, "guest_caller_chat_id", extra.GuestChat.ID)
	}
	fileName := ""
	if m.Video != nil {
		fileName = m.Video.FileName
	} else if m.Document != nil {
		fileName = m.Document.FileName
	}
	buttonCount, callbackCount := 0, 0
	if m.ReplyMarkup != nil {
		for _, row := range m.ReplyMarkup.InlineKeyboard {
			for _, b := range row {
				buttonCount++
				if b.CallbackData != nil {
					callbackCount++
				}
			}
		}
	}
	attrs = append(attrs, "has_file_name", fileName != "", "filename_youtube_id", telegram.FilenameYouTubeID(m), "inline_button_count", buttonCount, "callback_button_count", callbackCount)
	if m.SenderChat != nil {
		attrs = append(attrs, "sender_chat_id", m.SenderChat.ID)
	}
	if reply := m.ReplyToMessage; reply != nil {
		attrs = append(attrs, "reply_message_id", reply.MessageID, "reply_youtube_video_ids", youtubeIDs(reply), "reply_source_video_ids", telegram.SourceIDs(reply))
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

func youtubeIDs(m *tg.Message) []string { return telegram.YouTubeIDs(m) }
