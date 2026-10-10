package handler

import (
	"context"
	"log/slog"
	"net/http"
	"os"

	"github.com/Seraf-seraf/cringemeter/internal/adapters/webhook"
	"github.com/Seraf-seraf/cringemeter/internal/config"
	tg "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func Handler(w http.ResponseWriter, r *http.Request) {
	c, err := config.Load(os.Getenv)
	if err != nil {
		slog.Error("configuration invalid", "phase", "config", "code", "config_invalid")
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	webhook.Handler{Config: c, Process: func(context.Context, tg.Update) error { return nil }}.ServeHTTP(w, r)
}
