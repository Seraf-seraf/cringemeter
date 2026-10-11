package telegram

import (
	tg "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"testing"
)

func TestDownloadSourcesUseHumanReplyAndCanonicalIDs(t *testing.T) {
	m := &tg.Message{MessageID: 3, Chat: &tg.Chat{ID: -100}, From: &tg.User{ID: 99, IsBot: true}, Video: &tg.Video{FileUniqueID: "f", FileName: "abcdefghijk.mp4"}, ReplyToMessage: &tg.Message{MessageID: 2, Chat: &tg.Chat{ID: -100}, From: &tg.User{ID: 42}, Text: "https://youtu.be/abcdefghijk"}}
	in, ok := DownloadedIncoming(m, 99)
	if !ok || in.UploaderID != nil || in.SenderBotID != 99 || !in.AllowFIFO || in.SourceRequest == nil || in.SourceRequest.UserID != 42 || len(in.SourceVideoIDs) != 1 || in.SourceVideoIDs[0] != "abcdefghijk" {
		t.Fatal(in)
	}
	in, _ = DownloadedIncoming(m, 77)
	if in.UploaderID != nil || in.SourceRequest != nil || in.AllowFIFO {
		t.Fatal("untrusted bot was attributed", in)
	}
	m.From = &tg.User{ID: 42}
	in, _ = DownloadedIncoming(m, 99)
	if in.UploaderID == nil || *in.UploaderID != 42 || in.AllowFIFO {
		t.Fatal("human upload mapping", in)
	}
	m.SenderChat = &tg.Chat{ID: -100}
	in, _ = DownloadedIncoming(m, 99)
	if in.UploaderID != nil {
		t.Fatal("anonymous sender mapped to human")
	}
}

func TestSourceIDsFromLinksAndFilenameRejectGuesses(t *testing.T) {
	m := &tg.Message{Text: "https://youtube.com/shorts/abcdefghijk https://youtube.com.evil/watch?v=ZYXWVUTSRQP", Video: &tg.Video{FileName: "private-title abcdefghijk.mp4"}, ReplyMarkup: &tg.InlineKeyboardMarkup{InlineKeyboard: [][]tg.InlineKeyboardButton{{tg.NewInlineKeyboardButtonURL("Source", "https://youtu.be/ZYXWVUTSRQP")}}}}
	ids := YouTubeIDs(m)
	if len(ids) != 2 || ids[0] != "abcdefghijk" || ids[1] != "ZYXWVUTSRQP" || FilenameYouTubeID(m) != "" {
		t.Fatal(ids)
	}
	m.Video.FileName = "abcdefghijk.exe"
	if FilenameYouTubeID(m) != "" {
		t.Fatal("non-video filename accepted")
	}
	m.Video.FileName = "abcdefghijk.mp4"
	if FilenameYouTubeID(m) != "abcdefghijk" {
		t.Fatal("exact filename missing")
	}
}
