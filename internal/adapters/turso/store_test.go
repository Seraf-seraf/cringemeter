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
	counts, err := s.Counts(ctx, b.ID)
	if err != nil || counts.Cringe != 1 || counts.NonCringe != 0 {
		t.Fatal(counts, err)
	}
	other, _ := s.Counts(ctx, c.ID)
	if other.Total() != 0 {
		t.Fatal(other)
	}
	copies, err := s.Copies(ctx, a.ID, 0, 100)
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
	s.DB.QueryRow("SELECT COUNT(*) FROM author_votes").Scan(&votesN)
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
	counts, err := s.Counts(ctx, p.ID)
	if err != nil || counts.Total() != 10 {
		t.Fatal(counts, err)
	}
}

func ownedPost(t *testing.T, s *Store, source int, file string, owner int64) domain.Post {
	t.Helper()
	p, err := s.Register(context.Background(), domain.Incoming{ChatID: -100, MessageID: source, UniqueID: file, MediaType: "video", UploaderID: &owner})
	if err != nil {
		t.Fatal(err)
	}
	publish(t, s, p, 100+source)
	return p
}
func voteFor(t *testing.T, s *Store, p domain.Post, voter int64, value int) {
	t.Helper()
	if _, err := s.Vote(context.Background(), p.ID, p.ChatID, 100+p.SourceMessageID, voter, value, 10); err != nil {
		t.Fatal(err)
	}
}
func TestUserRankingSeparatesAuthorsDeduplicatesFilesAndBreaksTies(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := ownedPost(t, s, 1, "same", 50)
	aRepeat := ownedPost(t, s, 2, "same", 50)
	b := ownedPost(t, s, 3, "same", 10)
	aOther := ownedPost(t, s, 4, "other", 50)
	// Only the author under whose copy the vote was cast earns the point.
	voteFor(t, s, a, 7, 1)
	top, err := s.Top(ctx, -100)
	if err != nil || len(top) != 1 || top[0].UserID != 50 || top[0].Score != 1 {
		t.Fatal(top, err)
	}
	c, _ := s.Counts(ctx, b.ID)
	if c.Total() != 0 {
		t.Fatal("other author got vote", c)
	}
	copies, err := s.Copies(ctx, a.ID, 0, 10)
	if err != nil || len(copies) != 2 {
		t.Fatal(copies, err)
	}
	// Same voter, same author and same file remains one point.
	voteFor(t, s, aRepeat, 7, 1)
	voteFor(t, s, b, 7, 1)
	top, err = s.Top(ctx, -100)
	if err != nil || len(top) != 2 || top[0].UserID != 50 {
		t.Fatal(top, err)
	}
	// A second file adds to the author's absolute total; non-cringe does not.
	voteFor(t, s, aOther, 7, 1)
	voteFor(t, s, b, 8, 0)
	top, err = s.Top(ctx, -100)
	if err != nil || top[0].Score != 2 {
		t.Fatal(top, err)
	}
	// Losing a point and regaining it places the author after the incumbent.
	voteFor(t, s, aOther, 7, 0)
	voteFor(t, s, a, 7, 0)
	voteFor(t, s, aRepeat, 7, 1)
	top, err = s.Top(ctx, -100)
	if err != nil || top[0].UserID != 10 {
		t.Fatal(top, err)
	}
	voteFor(t, s, b, 7, 1) // Same-choice replay cannot change the tie order.
	top, err = s.Top(ctx, -100)
	if err != nil || top[0].UserID != 10 {
		t.Fatal(top, err)
	}
	// Repeat publication cannot reset its author's position.
	ownedPost(t, s, 5, "same", 10)
	top, err = s.Top(ctx, -100)
	if err != nil || top[0].UserID != 10 {
		t.Fatal(top, err)
	}
	for i := 6; i <= 8; i++ {
		p := ownedPost(t, s, i, "extra"+string(rune('a'+i)), int64(i))
		voteFor(t, s, p, 7, 1)
	}
	top, err = s.Top(ctx, -100)
	if err != nil || len(top) != 3 || top[0].UserID != 10 || top[1].UserID != 50 || top[2].UserID != 6 {
		t.Fatal(top, err)
	}
	top, err = s.Top(ctx, -200)
	if err != nil || len(top) != 0 {
		t.Fatal(top, err)
	}
}

func TestConcurrentUserScoresAreAtomicAndReplayStable(t *testing.T) {
	s := testStore(t)
	s.DB.SetMaxOpenConns(4)
	ctx := context.Background()
	a := ownedPost(t, s, 1, "a", 1)
	b := ownedPost(t, s, 2, "b", 2)
	var wg sync.WaitGroup
	for i := 1; i <= 40; i++ {
		for _, p := range []domain.Post{a, b} {
			wg.Add(1)
			go func(p domain.Post, v int64) {
				defer wg.Done()
				if _, err := s.Vote(ctx, p.ID, -100, 100+p.SourceMessageID, v, 1, 10); err != nil {
					t.Error(err)
				}
			}(p, int64(i))
		}
	}
	wg.Wait()
	top, err := s.Top(ctx, -100)
	if err != nil || len(top) != 2 || top[0].Score != 40 || top[1].Score != 40 {
		t.Fatal(top, err)
	}
	first := top[0].UserID
	voteFor(t, s, a, 1, 1)
	voteFor(t, s, b, 1, 1)
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	top, err = s.Top(ctx, -100)
	if err != nil || top[0].UserID != first {
		t.Fatal(top, err)
	}
	var mismatch int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM user_scores s JOIN score_positions p ON p.chat_id=s.chat_id AND p.user_id=s.user_id WHERE s.score!=p.score`).Scan(&mismatch); err != nil || mismatch != 0 {
		t.Fatal(mismatch, err)
	}
}

func TestSchemaCheckRejectsIncompatibleExistingTable(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "legacy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE media(id INTEGER PRIMARY KEY,file_unique_id TEXT)"); err != nil {
		t.Fatal(err)
	}
	s := &Store{DB: db}
	if err := s.Migrate(context.Background()); err == nil {
		t.Fatal("accepted schema without media uniqueness")
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE name='posts'").Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}

func TestMigrationResetsLegacyVotesOnceAndRejectsLateLegacyWrites(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "old.db")+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(initialSchema); err != nil {
		t.Fatal(err)
	}
	s := &Store{DB: db}
	ctx := context.Background()
	a, err := s.Register(ctx, domain.Incoming{ChatID: -100, MessageID: 1, MediaType: "video", UniqueID: "shared", UploaderID: ptr(50)})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Register(ctx, domain.Incoming{ChatID: -100, MessageID: 2, MediaType: "video", UniqueID: "shared", UploaderID: ptr(10)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO votes(media_id,user_id,vote,updated_at) VALUES(?,7,1,10),(?,8,0,11)`, a.MediaID, a.MediaID); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	counts, err := s.Counts(ctx, a.ID)
	if err != nil || counts.Total() != 0 {
		t.Fatal(counts, err)
	}
	counts, err = s.Counts(ctx, b.ID)
	if err != nil || counts.Total() != 0 {
		t.Fatal(counts, err)
	}
	top, err := s.Top(ctx, -100)
	if err != nil || len(top) != 0 {
		t.Fatal(top, err)
	}
	if _, err = db.Exec(`INSERT INTO votes(media_id,user_id,vote,updated_at) VALUES(?,9,1,12)`, a.MediaID); err == nil {
		t.Fatal("late old-deployment vote accepted without author")
	}

	publish(t, s, a, 101)
	publish(t, s, b, 102)
	voteFor(t, s, a, 7, 1)
	voteFor(t, s, b, 7, 1)
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	top, err = s.Top(ctx, -100)
	if err != nil || len(top) != 2 || top[0].UserID != 50 || top[1].UserID != 10 {
		t.Fatal(top, err)
	}
	var archive int
	if err = db.QueryRow(`SELECT COUNT(*) FROM votes`).Scan(&archive); err != nil || archive != 0 {
		t.Fatal(archive, err)
	}
}
func ptr(v int64) *int64 { return &v }
