package app

import (
	"context"
	"database/sql"
	"errors"
	"handler/internal/adapters/turso"
	"handler/internal/domain"
	_ "modernc.org/sqlite"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type fakeTelegram struct {
	mu                 sync.Mutex
	copies, deletes    int
	copyErr, deleteErr error
	beforeDelete       func()
}

func (f *fakeTelegram) Copy(context.Context, domain.Post, domain.Counts) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.copies++
	return 100 + f.copies, f.copyErr
}
func (f *fakeTelegram) Delete(context.Context, int64, int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deletes++
	if f.beforeDelete != nil {
		f.beforeDelete()
	}
	return f.deleteErr
}

func setup(t *testing.T) (*Service, *turso.Store, *fakeTelegram) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "bot.db")+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	store := &turso.Store{DB: db}
	t.Cleanup(func() { store.Close() })
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	messenger := &fakeTelegram{}
	return &Service{Store: store, Telegram: messenger, Now: func() time.Time { return time.Unix(10, 0) }}, store, messenger
}
func input(source int) domain.Incoming {
	return domain.Incoming{ChatID: -100, MessageID: source, MediaType: "video", UniqueID: "v"}
}

func TestPublishPersistsBeforeDeleteAndReplays(t *testing.T) {
	s, store, tg := setup(t)
	ctx := context.Background()
	tg.beforeDelete = func() {
		p, err := store.Post(ctx, 1)
		if err != nil || p.State != domain.Copied || p.BotMessageID != 101 {
			t.Fatal(p, err)
		}
	}
	if err := s.Publish(ctx, input(1)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := s.Publish(ctx, input(1)); err != nil {
			t.Fatal(err)
		}
	}
	p, _ := store.Post(ctx, 1)
	if p.State != domain.Done || tg.copies != 1 || tg.deletes != 1 {
		t.Fatal(p, tg)
	}
}

func TestCopyFailurePreservesOriginalAndNeverRetries(t *testing.T) {
	for _, tc := range []struct {
		name, state string
		err         error
	}{
		{"rejection", domain.CopyFailed, &domain.APIError{Code: 400, Confirmed: true}},
		{"network", domain.CopyUnknown, errors.New("lost response")},
		{"server", domain.CopyUnknown, &domain.APIError{Code: 500}},
		{"rate limit", domain.CopyFailed, &domain.APIError{Code: 429, Confirmed: true, RetryAfter: 5}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, store, tg := setup(t)
			tg.copyErr = tc.err
			ctx := context.Background()
			for i := 0; i < 2; i++ {
				if err := s.Publish(ctx, input(1)); err != nil {
					t.Fatal(err)
				}
			}
			p, _ := store.Post(ctx, 1)
			if p.State != tc.state || tg.copies != 1 || tg.deletes != 0 {
				t.Fatal(p, tg)
			}
		})
	}
}

func TestDeleteFailureRetriesOnlyDeletion(t *testing.T) {
	s, store, tg := setup(t)
	ctx := context.Background()
	tg.deleteErr = errors.New("delete failed")
	if err := s.Publish(ctx, input(1)); err == nil {
		t.Fatal("expected failure")
	}
	p, _ := store.Post(ctx, 1)
	if p.State != domain.Copied {
		t.Fatal(p)
	}
	tg.deleteErr = &domain.APIError{Code: 400, MessageGone: true, Confirmed: true}
	if err := s.Publish(ctx, input(1)); err != nil {
		t.Fatal(err)
	}
	p, _ = store.Post(ctx, 1)
	if p.State != domain.Done || tg.copies != 1 || tg.deletes != 2 {
		t.Fatal(p, tg)
	}
}

func TestConcurrentReplayMakesOneCopy(t *testing.T) {
	s, _, tg := setup(t)
	var wg sync.WaitGroup
	for i := 0; i < 15; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.Publish(context.Background(), input(1)); err != nil && !errors.Is(err, domain.ErrBusy) && !errors.Is(err, domain.ErrConflict) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if tg.copies != 1 {
		t.Fatal(tg.copies)
	}
}

func TestExpiredClaimDoesNotCopy(t *testing.T) {
	s, store, tg := setup(t)
	ctx := context.Background()
	p, err := store.Register(ctx, input(1))
	if err != nil {
		t.Fatal(err)
	}
	store.Claim(ctx, p.ID, 1, 9)
	if err := s.Publish(ctx, input(1)); err != nil {
		t.Fatal(err)
	}
	p, _ = store.Post(ctx, p.ID)
	if p.State != domain.CopyUnknown || tg.copies != 0 || tg.deletes != 0 {
		t.Fatal(p, tg)
	}
}
