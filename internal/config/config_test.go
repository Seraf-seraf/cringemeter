package config

import (
	"strings"
	"testing"
)

func TestLoad(t *testing.T) {
	base := map[string]string{"TELEGRAM_BOT_TOKEN": "123:example", "TELEGRAM_WEBHOOK_SECRET": "secret_-1", "TELEGRAM_CHAT_ID": "-100123", "TURSO_DATABASE_URL": "libsql://example.turso.io", "TURSO_AUTH_TOKEN": "test", "TELEGRAM_DOWNLOADER_BOT_ID": "99"}
	c, err := Load(func(k string) string { return base[k] })
	if err != nil || c.BotID != 123 || c.DownloaderBotID != 99 {
		t.Fatalf("defaults: %+v, %v", c, err)
	}
	for _, tc := range []struct{ key, value string }{
		{"TELEGRAM_DOWNLOADER_BOT_ID", "0"}, {"TELEGRAM_DOWNLOADER_BOT_ID", "-1"}, {"TELEGRAM_DOWNLOADER_BOT_ID", "not-an-id"}, {"TELEGRAM_BOT_TOKEN", ""}, {"TELEGRAM_BOT_TOKEN", "0:token"}, {"TELEGRAM_WEBHOOK_SECRET", ""}, {"TELEGRAM_WEBHOOK_SECRET", "bad!"}, {"TELEGRAM_WEBHOOK_SECRET", strings.Repeat("s", 257)}, {"TELEGRAM_CHAT_ID", "123"}, {"TURSO_DATABASE_URL", "http://example.com"}, {"TURSO_AUTH_TOKEN", ""},
	} {
		t.Run(tc.key+"/"+tc.value, func(t *testing.T) {
			_, err := Load(func(k string) string {
				if k == tc.key {
					return tc.value
				}
				return base[k]
			})
			if err == nil {
				t.Fatal("expected invalid configuration")
			}
		})
	}
	for _, n := range []int{1, 256} {
		_, err := Load(func(k string) string {
			if k == "TELEGRAM_WEBHOOK_SECRET" {
				return strings.Repeat("a", n)
			}
			return base[k]
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
