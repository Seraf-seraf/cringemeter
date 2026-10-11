package telegram

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	tg "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"handler/internal/domain"
)

func Participant(u *tg.User) (domain.Participant, bool) {
	if u == nil || u.ID <= 0 || u.IsBot {
		return domain.Participant{}, false
	}
	return domain.Participant{ID: u.ID, Username: u.UserName, Name: strings.TrimSpace(u.FirstName + " " + u.LastName)}, true
}

func ParseAuthorAction(q *tg.CallbackQuery) (domain.AuthorAction, bool) {
	if a, ok := AuthorClick(q); ok {
		return domain.AuthorAction{AuthorClick: a}, true
	}
	if q == nil || len(q.Data) > 64 {
		return domain.AuthorAction{}, false
	}
	parts := strings.Split(q.Data, ":")
	if len(parts) < 4 || parts[0] != "a" {
		return domain.AuthorAction{}, false
	}
	copy := *q
	copy.Data = "a:" + parts[2]
	click, ok := AuthorClick(&copy)
	if !ok {
		return domain.AuthorAction{}, false
	}
	a := domain.AuthorAction{AuthorClick: click}
	canonical := func(s string) (int64, bool) {
		n, e := strconv.ParseInt(s, 10, 64)
		return n, e == nil && n >= 0 && strconv.FormatInt(n, 10) == s
	}
	switch parts[1] {
	case "s":
		if len(parts) != 5 {
			return a, false
		}
		a.TargetID, ok = canonical(parts[3])
		if !ok || a.TargetID == 0 {
			return a, false
		}
		a.ExpectedRevision, ok = canonical(parts[4])
	case "l":
		if len(parts) != 5 {
			return a, false
		}
		n, valid := canonical(parts[3])
		if !valid || n > 100000 {
			return a, false
		}
		a.Page = int(n)
		a.ExpectedRevision, ok = canonical(parts[4])
	case "c":
		if len(parts) != 4 {
			return a, false
		}
		a.Cancel = true
		a.ExpectedRevision, ok = canonical(parts[3])
	default:
		return a, false
	}
	return a, ok
}

func (c *Client) LookupParticipant(ctx context.Context, chatID, userID int64) (domain.Participant, error) {
	var member struct {
		Status   string  `json:"status"`
		User     tg.User `json:"user"`
		IsMember bool    `json:"is_member"`
	}
	if err := c.Call(ctx, "getChatMember", map[string]any{"chat_id": chatID, "user_id": userID}, &member); err != nil {
		return domain.Participant{}, err
	}
	u, ok := Participant(&member.User)
	active := member.Status == "creator" || member.Status == "administrator" || member.Status == "member" || member.Status == "restricted" && member.IsMember
	if !ok || u.ID != userID || !active {
		return u, domain.ErrInvalid
	}
	return u, nil
}

func participantLabel(u domain.Participant) string {
	if u.Username != "" {
		return "@" + u.Username
	}
	if u.Name != "" {
		return u.Name
	}
	if u.ID > 0 {
		return "Пользователь " + strconv.FormatInt(u.ID, 10)
	}
	return "не определён"
}

func (c *Client) keyboard(ctx context.Context, p domain.Post, counts domain.Counts) tg.InlineKeyboardMarkup {
	u := domain.Participant{ID: p.UploaderID, Username: p.AuthorUsername, Name: p.AuthorName}
	if u.ID > 0 && u.Username == "" && u.Name == "" {
		lookup, cancel := context.WithTimeout(ctx, 400*time.Millisecond)
		defer cancel()
		if found, err := c.LookupParticipant(lookup, p.ChatID, u.ID); err == nil {
			u = found
		}
	}
	return Keyboard(p.ID, counts, "👤 Автор: "+participantLabel(u))
}

func (c *Client) ChooseAuthor(ctx context.Context, p domain.Post, counts domain.Counts, page domain.ParticipantPage) error {
	markup := c.keyboard(ctx, p, counts)
	users := append([]domain.Participant(nil), page.Users...)
	lookup, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for i := range users {
		if users[i].Username != "" || users[i].Name != "" {
			continue
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if u, e := c.LookupParticipant(lookup, p.ChatID, users[i].ID); e == nil {
				users[i] = u
			}
		}(i)
	}
	wg.Wait()
	for _, u := range users {
		markup.InlineKeyboard = append(markup.InlineKeyboard, tg.NewInlineKeyboardRow(tg.NewInlineKeyboardButtonData(participantLabel(u), fmt.Sprintf("a:s:%d:%d:%d", p.ID, u.ID, p.AuthorRevision))))
	}
	previous, next := page.Index, page.Index
	if previous > 0 {
		previous--
	}
	if next+1 < page.Pages {
		next++
	}
	navigation := tg.NewInlineKeyboardRow(
		tg.NewInlineKeyboardButtonData("←", fmt.Sprintf("a:l:%d:%d:%d", p.ID, previous, p.AuthorRevision)),
		tg.NewInlineKeyboardButtonData(fmt.Sprintf("%d/%d", page.Index+1, page.Pages), fmt.Sprintf("a:l:%d:%d:%d", p.ID, page.Index, p.AuthorRevision)),
		tg.NewInlineKeyboardButtonData("→", fmt.Sprintf("a:l:%d:%d:%d", p.ID, next, p.AuthorRevision)))
	markup.InlineKeyboard = append(markup.InlineKeyboard, navigation)
	markup.InlineKeyboard = append(markup.InlineKeyboard, tg.NewInlineKeyboardRow(tg.NewInlineKeyboardButtonData("Закрыть", fmt.Sprintf("a:c:%d:%d", p.ID, p.AuthorRevision))))
	return c.Call(ctx, "editMessageReplyMarkup", map[string]any{"chat_id": p.ChatID, "message_id": p.BotMessageID, "reply_markup": markup}, nil)
}

func AuthorClick(q *tg.CallbackQuery) (domain.AuthorClick, bool) {
	if q == nil || q.Message == nil || q.Message.Chat == nil || q.From == nil || q.From.ID <= 0 || q.ID == "" || len(q.Data) > 64 {
		return domain.AuthorClick{}, false
	}
	parts := strings.Split(q.Data, ":")
	if len(parts) != 2 || parts[0] != "a" {
		return domain.AuthorClick{}, false
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != parts[1] {
		return domain.AuthorClick{}, false
	}
	return domain.AuthorClick{CallbackID: q.ID, PostID: id, ChatID: q.Message.Chat.ID, MessageID: q.Message.MessageID, UserID: q.From.ID}, true
}

// Check the caller's current group role; never trust cached bot admin lists.
func (c *Client) IsAdmin(ctx context.Context, chatID, userID int64) (bool, error) {
	var member struct {
		Status string  `json:"status"`
		User   tg.User `json:"user"`
	}
	err := c.Call(ctx, "getChatMember", map[string]any{"chat_id": chatID, "user_id": userID}, &member)
	if err != nil {
		return false, err
	}
	return member.User.ID == userID && (member.Status == "creator" || member.Status == "administrator"), nil
}
