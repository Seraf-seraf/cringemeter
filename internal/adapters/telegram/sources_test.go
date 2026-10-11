package telegram

import (
	tg "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"reflect"
	"testing"
)

func TestDownloadLinksUseCanonicalSourceIDs(t *testing.T) {
	for _, tc := range []struct {
		url string
		id  string
	}{
		{"https://www.instagram.com/reel/Da7A8MPMm0n/?rpxt=Y2gycG1xcGFqbm9o", "instagram:Da7A8MPMm0n"},
		{"https://instagram.com/reel/Da7A8MPMm0n/", "instagram:Da7A8MPMm0n"},
		{"https://www.instagram.com/reel/DeWbEIHsceW/?vrfl=bXNtcHZwaDdsMzA5", "instagram:DeWbEIHsceW"},
		{"https://m.instagram.com/p/DeWbEIHsceW/?igsh=tracking#fragment", "instagram:DeWbEIHsceW"},
		{"https://www.tiktok.com/@author/video/7412345678901234567?is_from_webapp=1", "tiktok:7412345678901234567"},
		{"https://vm.tiktok.com/ZMabcdef/?share=tracking", "tiktok-share:ZMabcdef"},
		{"https://vt.tiktok.com/ZMabcdef/", "tiktok-share:ZMabcdef"},
		{"https://www.tiktok.com/t/ZMabcdef/", "tiktok-share:ZMabcdef"},
		{"https://youtube.com/watch?v=abcdefghijk&feature=shared", "abcdefghijk"},
		{"https://youtu.be/abcdefghijk?si=tracking", "abcdefghijk"},
	} {
		t.Run(tc.url, func(t *testing.T) {
			m := &tg.Message{MessageID: 2, Date: 10, Chat: &tg.Chat{ID: -100}, From: &tg.User{ID: 42}, Text: tc.url}
			r, ok := LinkRequest(m)
			if !ok || r.UserID != 42 || !reflect.DeepEqual(r.VideoIDs, []string{tc.id}) {
				t.Fatalf("link is not attributed by canonical ID: %+v, accepted=%v", r, ok)
			}
		})
	}
}

func TestDownloadLinksRejectUnsupportedHostsAndProfiles(t *testing.T) {
	for _, value := range []string{
		"https://instagram.com.evil/reel/Da7A8MPMm0n/",
		"https://evilinstagram.com/reel/Da7A8MPMm0n/",
		"https://instagram.com@evil.example/reel/Da7A8MPMm0n/",
		"https://www.instagram.com/author/",
		"https://www.tiktok.com/@author",
		"https://example.com/video/7412345678901234567",
		"https://youtube.com/watch?v=instagram%3ADa7A8MPMm0n",
		"https://youtu.be/tiktok:7412345678901234567",
	} {
		m := &tg.Message{MessageID: 2, Chat: &tg.Chat{ID: -100}, From: &tg.User{ID: 42}, Text: value}
		if _, ok := LinkRequest(m); ok {
			t.Fatal("unsupported source accepted", value)
		}
	}
}

func TestInstagramDownloadCorrelatesReplyCaptionAndButtonWithoutQuery(t *testing.T) {
	reply := &tg.Message{MessageID: 2, Date: 10, Chat: &tg.Chat{ID: -100}, From: &tg.User{ID: 42}, Text: "https://www.instagram.com/reel/Da7A8MPMm0n/?rpxt=tracking"}
	m := &tg.Message{MessageID: 3, Chat: reply.Chat, From: &tg.User{ID: 99, IsBot: true}, Video: &tg.Video{FileUniqueID: "f", FileName: "Da7A8MPMm0n.mp4"}, ReplyToMessage: reply,
		Caption: "https://instagram.com/p/Da7A8MPMm0n/?vrfl=other", ReplyMarkup: &tg.InlineKeyboardMarkup{InlineKeyboard: [][]tg.InlineKeyboardButton{{tg.NewInlineKeyboardButtonURL("Source", "https://instagram.com/reel/Da7A8MPMm0n/")}}}}
	in, ok := DownloadedIncoming(m, 99)
	if !ok || in.SourceRequest == nil || in.SourceRequest.UserID != 42 || !reflect.DeepEqual(in.SourceRequest.VideoIDs, []string{"instagram:Da7A8MPMm0n"}) || !reflect.DeepEqual(in.SourceVideoIDs, in.SourceRequest.VideoIDs) || in.SourceEvidence != "source_url" {
		t.Fatal("Instagram source metadata did not match the human request", in)
	}
	reply.Text = "source"
	reply.Entities = []tg.MessageEntity{{Type: "text_link", URL: "https://instagram.com/reel/Da7A8MPMm0n/?igsh=other"}}
	r, ok := LinkRequest(reply)
	if !ok || !reflect.DeepEqual(r.VideoIDs, in.SourceVideoIDs) {
		t.Fatal("hidden source link was not recognized", r, ok)
	}
	if got := YouTubeIDs(m); len(got) != 0 {
		t.Fatal("Instagram shortcode leaked into YouTube IDs", got)
	}
}

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
