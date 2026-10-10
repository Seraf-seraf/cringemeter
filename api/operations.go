package handler

import (
	"errors"
	"handler/internal/adapters/operations"
	"handler/internal/adapters/turso"
	"net/http"
	"os"
)

func Operations(w http.ResponseWriter, r *http.Request) {
	operations.Handler{Secret: os.Getenv("CRINGEMETER_ADMIN_SECRET"), Open: func() (*turso.Store, error) {
		databaseURL, token := os.Getenv("TURSO_DATABASE_URL"), os.Getenv("TURSO_AUTH_TOKEN")
		if databaseURL == "" || token == "" {
			return nil, errors.New("database settings missing")
		}
		return turso.Open(databaseURL, token)
	}}.ServeHTTP(w, r)
}
