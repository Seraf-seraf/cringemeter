package telegram

import (
	"path"
	"regexp"
	"strings"

	tg "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"handler/internal/domain"
)

var sourceURL = regexp.MustCompile(`(?i)https?://[^\s<>()]+`)

// YouTubeIDs extracts canonical IDs, never titles, arbitrary callback data or
// opaque Telegram file identifiers. The result is bounded by message size.
func YouTubeIDs(m *tg.Message) []string {
	ids := make([]string, 0)
	for _, id := range SourceIDs(m) {
		if domain.ValidYouTubeID(id) {
			ids = append(ids, id)
		}
	}
	return ids
}

func SourceIDs(m *tg.Message) []string {
	ids := make([]string, 0)
	if m == nil {
		return ids
	}
	urls := sourceURL.FindAllString(m.Text+" "+m.Caption, -1)
	for _, entities := range [][]tg.MessageEntity{m.Entities, m.CaptionEntities} {
		for _, e := range entities {
			if e.Type == "text_link" {
				urls = append(urls, e.URL)
			}
		}
	}
	if m.ReplyMarkup != nil {
		for _, row := range m.ReplyMarkup.InlineKeyboard {
			for _, b := range row {
				if b.URL != nil {
					urls = append(urls, *b.URL)
				}
			}
		}
	}
	for _, value := range urls {
		if id := domain.SourceVideoID(strings.TrimRight(value, ".,;!?")); id != "" {
			ids = appendUniqueID(ids, id)
		}
	}
	return ids
}

func appendUniqueID(ids []string, id string) []string {
	for _, existing := range ids {
		if existing == id {
			return ids
		}
	}
	return append(ids, id)
}

func FilenameYouTubeID(m *tg.Message) string {
	name := ""
	switch {
	case m.Video != nil:
		name = m.Video.FileName
	case m.Document != nil:
		name = m.Document.FileName
	case m.Animation != nil:
		name = m.Animation.FileName
	}
	ext := path.Ext(name)
	if ext != ".mp4" && ext != ".webm" && ext != ".mkv" && ext != ".mov" {
		return ""
	}
	id := strings.TrimSuffix(name, ext)
	if domain.ValidYouTubeID(id) {
		return id
	}
	return ""
}

func LinkRequest(m *tg.Message) (domain.VideoRequest, bool) {
	if m == nil || m.Chat == nil || m.MessageID <= 0 || m.From == nil || m.From.ID <= 0 || m.From.IsBot || m.SenderChat != nil || m.Text == "" {
		return domain.VideoRequest{}, false
	}
	ids := SourceIDs(m)
	if len(ids) == 0 {
		return domain.VideoRequest{}, false
	}
	return domain.VideoRequest{ChatID: m.Chat.ID, MessageID: m.MessageID, UserID: m.From.ID, VideoIDs: ids, SentAt: int64(m.Date)}, true
}

func DownloadedIncoming(m *tg.Message, loaderID int64) (domain.Incoming, bool) {
	in, ok := Incoming(m)
	if !ok {
		return in, false
	}
	if m.From == nil || !m.From.IsBot {
		return in, true
	}
	in.UploaderID = nil
	in.SenderBotID = m.From.ID
	if loaderID <= 0 || m.From.ID != loaderID || m.SenderChat != nil || m.Video == nil {
		return in, true
	}
	in.AllowFIFO = true
	if r, valid := LinkRequest(m.ReplyToMessage); valid && r.ChatID == in.ChatID {
		in.SourceRequest = &r
	}
	in.SourceVideoIDs = SourceIDs(m)
	if len(in.SourceVideoIDs) > 0 {
		in.SourceEvidence = "source_url"
		if len(in.SourceVideoIDs) == 1 && domain.ValidYouTubeID(in.SourceVideoIDs[0]) {
			in.SourceEvidence = "youtube_url"
		}
	}
	filenameAllowed := true
	for _, id := range in.SourceVideoIDs {
		if !domain.ValidYouTubeID(id) {
			filenameAllowed = false
		}
	}
	if id := FilenameYouTubeID(m); id != "" && filenameAllowed {
		in.SourceVideoIDs = appendUniqueID(in.SourceVideoIDs, id)
		if in.SourceEvidence == "" {
			in.SourceEvidence = "youtube_filename"
		}
	}
	return in, true
}
