package handler

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	tg "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"handler/internal/adapters/telegram"
	"handler/internal/adapters/turso"
	"handler/internal/adapters/webhook"
	"handler/internal/app"
	"handler/internal/config"
	"handler/internal/domain"
)

func Handler(w http.ResponseWriter, r *http.Request) {
	c, err := config.Load(os.Getenv)
	if err != nil {
		slog.Error("configuration invalid", "phase", "config", "code", "config_invalid")
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	webhook.Handler{Config: c, Process: func(ctx context.Context, u tg.Update) error { return process(ctx, c, u) }}.ServeHTTP(w, r)
}

func process(ctx context.Context, c config.Config, u tg.Update) error {
	client := telegram.New(c.BotToken)
	var incoming domain.Incoming
	var ballot domain.Ballot
	var authorAction domain.AuthorAction
	var isAuthor bool
	var group domain.Group
	var isMedia, isTop, isRequest bool
	var request domain.VideoRequest
	if u.Message != nil {
		incoming, isMedia = telegram.DownloadedIncoming(u.Message, c.DownloaderBotID)
		request, isRequest = telegram.LinkRequest(u.Message)
		if !isMedia && !isRequest {
			address, ok := telegram.TopCommand(u.Message)
			if ok && address != "" {
				username, err := client.Username(ctx)
				if err != nil {
					return err
				}
				if !strings.EqualFold(username, address) {
					return nil
				}
			}
			isTop = ok
			group = domain.Group{ID: u.Message.Chat.ID, Type: u.Message.Chat.Type, Username: u.Message.Chat.UserName}
			if !ok {
				if _, human := telegram.Participant(u.Message.From); !human || u.Message.SenderChat != nil {
					return nil
				}
			}
		}
	} else if u.CallbackQuery != nil {
		var ok bool
		authorAction, isAuthor = telegram.ParseAuthorAction(u.CallbackQuery)
		if !isAuthor {
			ballot, ok = telegram.Ballot(u.CallbackQuery)
		} else {
			ok = true
		}
		if !ok {
			return client.Answer(ctx, u.CallbackQuery.ID, "Кнопка недоступна", false)
		}
	} else {
		return nil
	}
	store, err := turso.Open(c.DatabaseURL, c.AuthToken)
	if err != nil {
		return err
	}
	defer store.Close()
	if u.Message != nil && u.Message.SenderChat == nil {
		if actor, valid := telegram.Participant(u.Message.From); valid {
			if err = store.RememberParticipant(ctx, c.ChatID, actor); err != nil {
				return err
			}
		}
		if incoming.SourceRequest != nil && u.Message.ReplyToMessage != nil {
			if actor, valid := telegram.Participant(u.Message.ReplyToMessage.From); valid {
				if err = store.RememberParticipant(ctx, c.ChatID, actor); err != nil {
					return err
				}
			}
		}
	}
	if u.CallbackQuery != nil && !isAuthor {
		if actor, valid := telegram.Participant(u.CallbackQuery.From); valid {
			if err = store.RememberParticipant(ctx, c.ChatID, actor); err != nil {
				return err
			}
		}
	}
	if isRequest && !isMedia {
		return store.RememberRequest(ctx, request)
	}
	service := app.Service{Store: store, Telegram: client, Now: time.Now}
	if isAuthor {
		return service.ManageAuthor(ctx, authorAction)
	}
	if isMedia {
		return service.Publish(ctx, incoming)
	}
	if isTop {
		return service.Ranking(ctx, group)
	}
	if u.CallbackQuery != nil {
		return service.RecordVote(ctx, ballot)
	}
	return nil
}
