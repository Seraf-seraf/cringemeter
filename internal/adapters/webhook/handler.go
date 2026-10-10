package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	tg "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"handler/internal/adapters/httpauth"
	"handler/internal/adapters/httpjson"
	"handler/internal/config"
)

const MaxBodyBytes = 1 << 20
const RequestTimeout = 8 * time.Second

type Handler struct {
	Config  config.Config
	Process func(context.Context, tg.Update) error
}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/webhook" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if !httpauth.Matches(h.Config.WebhookSecret, r.Header.Get("X-Telegram-Bot-Api-Secret-Token")) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
	defer r.Body.Close()
	var envelope struct {
		ID       *int            `json:"update_id"`
		Message  json.RawMessage `json:"message"`
		Callback json.RawMessage `json:"callback_query"`
		Member   json.RawMessage `json:"my_chat_member"`
	}
	err := httpjson.DecodeObject(r.Body, &envelope)
	if err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, "invalid update", http.StatusBadRequest)
		}
		return
	}
	types := 0
	for _, payload := range []json.RawMessage{envelope.Message, envelope.Callback, envelope.Member} {
		if len(payload) > 0 {
			types++
		}
	}
	if envelope.ID == nil || *envelope.ID < 0 || types != 1 {
		http.Error(w, "invalid update", http.StatusBadRequest)
		return
	}
	// Queued membership events can predate allowed_updates registration.
	if len(envelope.Member) > 0 {
		var member *tg.ChatMemberUpdated
		if json.Unmarshal(envelope.Member, &member) != nil || member == nil || member.Chat.ID == 0 {
			http.Error(w, "invalid membership update", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
		return
	}
	u := tg.Update{UpdateID: *envelope.ID}
	if len(envelope.Message) > 0 {
		if json.Unmarshal(envelope.Message, &u.Message) != nil || u.Message == nil || u.Message.Chat == nil || u.Message.MessageID <= 0 {
			http.Error(w, "invalid message", http.StatusBadRequest)
			return
		}
		if u.Message.Chat.ID != h.Config.ChatID || (u.Message.Chat.Type != "group" && u.Message.Chat.Type != "supergroup") || (u.Message.From != nil && u.Message.From.ID == h.Config.BotID) {
			w.WriteHeader(http.StatusOK)
			return
		}
	} else {
		if json.Unmarshal(envelope.Callback, &u.CallbackQuery) != nil || u.CallbackQuery == nil || u.CallbackQuery.ID == "" || u.CallbackQuery.From == nil || u.CallbackQuery.From.ID <= 0 {
			http.Error(w, "invalid callback", http.StatusBadRequest)
			return
		}
		m := u.CallbackQuery.Message
		if m == nil || m.Chat == nil || m.Chat.ID != h.Config.ChatID || (m.Chat.Type != "group" && m.Chat.Type != "supergroup") {
			w.WriteHeader(http.StatusOK)
			return
		}
		if m.MessageID <= 0 || m.From == nil || m.From.ID != h.Config.BotID {
			w.WriteHeader(http.StatusOK)
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), RequestTimeout)
	defer cancel()
	if err := h.Process(ctx, u); err != nil {
		slog.ErrorContext(ctx, "update failed", "update_id", u.UpdateID, "phase", "process", "code", "processing_failed")
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
}
