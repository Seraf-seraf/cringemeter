package main

import (
	"context"
	"handler/internal/adapters/turso"
	"log/slog"
	"os"
	"time"
)

func main() {
	databaseURL, token := os.Getenv("TURSO_DATABASE_URL"), os.Getenv("TURSO_AUTH_TOKEN")
	if databaseURL == "" || token == "" {
		slog.Error("database settings missing")
		os.Exit(1)
	}
	store, err := turso.Open(databaseURL, token)
	if err == nil {
		defer store.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err = store.Migrate(ctx)
	}
	if err != nil {
		slog.Error("migration failed", "code", "migration_failed")
		os.Exit(1)
	}
	slog.Info("schema ready", "version", 1)
}
