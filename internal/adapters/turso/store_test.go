package turso

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/WillowWorks-io/libsql-client-go/libsql"
	"handler/internal/domain"
	_ "modernc.org/sqlite"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	connector, err := libsql.NewConnector("file:" + filepath.Join(t.TempDir(), "votes.db") + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	s := &Store{DB: sql.OpenDB(connector)}
	s.DB.SetMaxOpenConns(1)
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}

func register(t *testing.T, s *Store, source int, file string) domain.Post {
	t.Helper()
	p, err := s.Register(context.Background(), domain.Incoming{ChatID: -100, MessageID: source, UniqueID: file, MediaType: "video"})
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func publish(t *testing.T, s *Store, p domain.Post, message int) {
	t.Helper()
	ctx := context.Background()
	ok, err := s.Claim(ctx, p.ID, 1, 31)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err := s.CopyResult(ctx, p.ID, message, domain.Copied, 2); err != nil {
		t.Fatal(err)
	}
}

func TestMediaPostsVotes(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	a, b, c := register(t, s, 1, "same"), register(t, s, 2, "same"), register(t, s, 3, "other")
	again := register(t, s, 1, "same")
	if a.MediaID != b.MediaID || a.MediaID == c.MediaID || a.ID != again.ID || a.ID == b.ID {
		t.Fatal(a, b, c, again)
	}
	publish(t, s, a, 101)
	publish(t, s, b, 102)
	publish(t, s, c, 103)
	for _, v := range []int{1, 1, 0} {
		if _, err := s.Vote(ctx, a.ID, -100, 101, 7, v, 10); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Vote(ctx, b.ID, -100, 102, 7, 1, 11); err != nil {
		t.Fatal(err)
	}
	counts, err := s.Counts(ctx, b.MediaID)
	if err != nil || counts.Cringe != 1 || counts.NonCringe != 0 {
		t.Fatal(counts, err)
	}
	other, _ := s.Counts(ctx, c.MediaID)
	if other.Total() != 0 {
		t.Fatal(other)
	}
	copies, err := s.Copies(ctx, a.MediaID, 0, 100)
	if err != nil || len(copies) != 2 {
		t.Fatal(copies, err)
	}
	if _, err := s.Vote(ctx, a.ID, -200, 101, 7, 1, 12); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("foreign chat", err)
	}
	if _, err := s.Vote(ctx, a.ID, -100, 102, 7, 1, 12); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("wrong copy", err)
	}
	if _, err := s.Vote(ctx, a.ID, -100, 101, 7, 2, 12); !errors.Is(err, domain.ErrInvalid) {
		t.Fatal(err)
	}
	var mediaN, postsN, votesN int
	s.DB.QueryRow("SELECT COUNT(*) FROM media").Scan(&mediaN)
	s.DB.QueryRow("SELECT COUNT(*) FROM posts").Scan(&postsN)
	s.DB.QueryRow("SELECT COUNT(*) FROM votes").Scan(&votesN)
	if mediaN != 2 || postsN != 3 || votesN != 1 {
		t.Fatal(mediaN, postsN, votesN)
	}
	if _, err := s.DB.Exec("INSERT INTO votes(media_id,user_id,vote) VALUES(999,1,1)"); err == nil {
		t.Fatal("foreign key not enforced")
	}
}

func TestClaimsAndCopyStates(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	p := register(t, s, 1, "v")
	var wg sync.WaitGroup
	wins := make(chan bool, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := s.Claim(ctx, p.ID, 10, 40)
			if err != nil {
				t.Error(err)
			}
			wins <- ok
		}()
	}
	wg.Wait()
	close(wins)
	n := 0
	for ok := range wins {
		if ok {
			n++
		}
	}
	if n != 1 {
		t.Fatal("claim winners", n)
	}
	if ok, err := s.Claim(ctx, p.ID, 40, 70); err != nil || ok {
		t.Fatal(ok, err)
	}
	stored, _ := s.Post(ctx, p.ID)
	if stored.State != domain.CopyUnknown {
		t.Fatal(stored)
	}
	if err := s.CopyResult(ctx, p.ID, 10, domain.Copied, 41); err != nil {
		t.Fatal(err)
	}
	if err := s.Deleted(ctx, p.ID, 42); err != nil {
		t.Fatal(err)
	}
	stored, _ = s.Post(ctx, p.ID)
	if stored.State != domain.Done || stored.BotMessageID != 10 {
		t.Fatal(stored)
	}
	if err := s.CopyResult(ctx, p.ID, 11, domain.Copied, 43); !errors.Is(err, domain.ErrConflict) {
		t.Fatal(err)
	}
}

func TestConcurrentVotes(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	p := register(t, s, 1, "v")
	publish(t, s, p, 101)
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := s.Vote(ctx, p.ID, -100, 101, int64(i%10+1), i%2, 10); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	counts, err := s.Counts(ctx, p.MediaID)
	if err != nil || counts.Total() != 10 {
		t.Fatal(counts, err)
	}
}

func TestTopUsesUniqueMediaAndThreshold(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a, b, c := register(t, s, 1, "same"), register(t, s, 2, "same"), register(t, s, 3, "other")
	publish(t, s, a, 101)
	publish(t, s, b, 102)
	publish(t, s, c, 103)
	for i := 1; i <= 4; i++ {
		s.Vote(ctx, a.ID, -100, 101, int64(i), 1, 10)
	}
	top, err := s.Top(ctx, -100, 5, 10)
	if err != nil || len(top) != 0 {
		t.Fatal(top, err)
	}
	s.Vote(ctx, b.ID, -100, 102, 5, 1, 10)
	for i := 1; i <= 6; i++ {
		s.Vote(ctx, c.ID, -100, 103, int64(i), 1, 10)
	}
	top, err = s.Top(ctx, -100, 5, 10)
	if err != nil || len(top) != 2 || top[0].MediaID != c.MediaID || top[1].MediaID != a.MediaID || top[1].MessageID != 102 {
		t.Fatal(top, err)
	}
	top, err = s.Top(ctx, -100, 5, 1)
	if err != nil || len(top) != 1 {
		t.Fatal(top, err)
	}
	// Equal totals and ratio use increasing media ID as the last tie breaker.
	s.Vote(ctx, b.ID, -100, 102, 6, 1, 10)
	top, err = s.Top(ctx, -100, 5, 10)
	if err != nil || top[0].MediaID != a.MediaID {
		t.Fatal(top, err)
	}
}
