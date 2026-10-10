package config

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
)

type Config struct {
	BotToken      string
	BotID         int64
	WebhookSecret string
	ChatID        int64
	DatabaseURL   string
	AuthToken     string
	TopLimit      int
	TopMinVotes   int
}

func Load(getenv func(string) string) (Config, error) {
	c := Config{BotToken: getenv("TELEGRAM_BOT_TOKEN"), WebhookSecret: getenv("TELEGRAM_WEBHOOK_SECRET"), DatabaseURL: getenv("TURSO_DATABASE_URL"), AuthToken: getenv("TURSO_AUTH_TOKEN"), TopLimit: 10, TopMinVotes: 5}
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
	if v := getenv("TOP_LIMIT"); v != "" {
		c.TopLimit, err = strconv.Atoi(v)
		if err != nil || c.TopLimit < 1 || c.TopLimit > 10 {
			return Config{}, errors.New("invalid TOP_LIMIT")
		}
	}
	if v := getenv("TOP_MIN_VOTES"); v != "" {
		c.TopMinVotes, err = strconv.Atoi(v)
		if err != nil || c.TopMinVotes < 5 {
			return Config{}, errors.New("invalid TOP_MIN_VOTES")
		}
	}
	return c, nil
}
