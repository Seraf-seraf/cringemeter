package config

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
)

type Config struct {
	BotToken        string
	BotID           int64
	WebhookSecret   string
	ChatID          int64
	DatabaseURL     string
	AuthToken       string
	DebugUploads    bool
	DownloaderBotID int64
}

func Load(getenv func(string) string) (Config, error) {
	c := Config{BotToken: getenv("TELEGRAM_BOT_TOKEN"), WebhookSecret: getenv("TELEGRAM_WEBHOOK_SECRET"), DatabaseURL: getenv("TURSO_DATABASE_URL"), AuthToken: getenv("TURSO_AUTH_TOKEN")}
	c.DebugUploads = getenv("TELEGRAM_DEBUG_UPLOADS") == "1"
	if value := getenv("TELEGRAM_DOWNLOADER_BOT_ID"); value != "" {
		var err error
		c.DownloaderBotID, err = strconv.ParseInt(value, 10, 64)
		if err != nil || c.DownloaderBotID <= 0 {
			return Config{}, errors.New("invalid TELEGRAM_DOWNLOADER_BOT_ID")
		}
	}
	parts := strings.Split(c.BotToken, ":")
	if len(parts) != 2 || parts[1] == "" {
		return Config{}, errors.New("invalid TELEGRAM_BOT_TOKEN")
	}
	var err error
	c.BotID, err = strconv.ParseInt(parts[0], 10, 64)
	if err != nil || c.BotID <= 0 {
		return Config{}, errors.New("invalid TELEGRAM_BOT_TOKEN")
	}
	if len(c.WebhookSecret) < 1 || len(c.WebhookSecret) > 256 {
		return Config{}, errors.New("invalid TELEGRAM_WEBHOOK_SECRET")
	}
	for _, b := range []byte(c.WebhookSecret) {
		if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_' || b == '-') {
			return Config{}, errors.New("invalid TELEGRAM_WEBHOOK_SECRET")
		}
	}
	c.ChatID, err = strconv.ParseInt(getenv("TELEGRAM_CHAT_ID"), 10, 64)
	if err != nil || c.ChatID >= 0 {
		return Config{}, errors.New("invalid TELEGRAM_CHAT_ID")
	}
	u, err := url.Parse(c.DatabaseURL)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "libsql" && u.Scheme != "https") {
		return Config{}, errors.New("invalid TURSO_DATABASE_URL")
	}
	if strings.TrimSpace(c.AuthToken) == "" {
		return Config{}, errors.New("missing TURSO_AUTH_TOKEN")
	}
	return c, nil
}
