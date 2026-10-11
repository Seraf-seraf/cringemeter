package handler

import (
	"errors"
	"handler/internal/adapters/operations"
	"handler/internal/adapters/turso"
	"net/http"
	"os"
	"strconv"
)

func Operations(w http.ResponseWriter, r *http.Request) {
	chatID, _ := strconv.ParseInt(os.Getenv("TELEGRAM_CHAT_ID"), 10, 64)
	loaderID, _ := strconv.ParseInt(os.Getenv("TELEGRAM_DOWNLOADER_BOT_ID"), 10, 64)
	operations.Handler{Secret: os.Getenv("CRINGEMETER_ADMIN_SECRET"), ChatID: chatID, DownloaderBotID: loaderID, Open: func() (*turso.Store, error) {
		databaseURL, token := os.Getenv("TURSO_DATABASE_URL"), os.Getenv("TURSO_AUTH_TOKEN")
		if databaseURL == "" || token == "" {
			return nil, errors.New("database settings missing")
		}
		return turso.Open(databaseURL, token)
	}}.ServeHTTP(w, r)
}
