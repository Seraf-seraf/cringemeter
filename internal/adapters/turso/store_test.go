package turso

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

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
	s := &Store{DB: sql.OpenDB(connector), now: func() time.Time { return time.Unix(10, 0) }}
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

func TestWilsonTopConsidersAllAuthorsBeforeChoosingTen(t *testing.T) {
	s := testStore(t)
	for i := 1; i <= 11; i++ {
		p := ownedPost(t, s, i, "same-video", int64(i))
		votes := 1
		if i == 11 {
			votes = 10
		}
		for v := 0; v < votes; v++ {
			value := 1
			if v == 9 {
				value = 0
			}
			voteFor(t, s, p, int64(1000+v), value)
		}
	}
	top, err := s.Top(context.Background(), -100)
	if err != nil || len(top) != 10 || top[0].UserID != 11 || top[0].Percentage != 90 || top[0].Votes != 10 || top[1].UserID != 1 || top[1].Votes != 1 || top[2].UserID != 2 {
		t.Fatal("9/10 must outrank all ten 1/1 authors", top, err)
	}
	if top[0].WilsonScore <= top[1].WilsonScore {
		t.Fatal("ranking used the displayed percentage", top)
	}
	repeat := ownedPost(t, s, 12, "same-video", 11)
	voteFor(t, s, repeat, 1000, 1)
	again, err := s.Top(context.Background(), -100)
	if err != nil || len(again) != len(top) {
		t.Fatal(again, err)
	}
	for i := range top {
		if again[i] != top[i] {
			t.Fatal("repeated file or vote changed the ranking", top, again)
		}
	}
}

func TestTopAuthorLimit(t *testing.T) {
	for _, count := range []int{0, 9, 10, 11} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			s := testStore(t)
			for i := count; i > 0; i-- {
				p := ownedPost(t, s, i, "same-video", int64(i))
				voteFor(t, s, p, 1000, 1)
			}
			want := min(count, 10)
			for attempt := 0; attempt < 2; attempt++ {
				top, err := s.Top(context.Background(), -100)
				if err != nil || len(top) != want {
					t.Fatal("incorrect author limit", top, err)
				}
				for i, rank := range top {
					if rank.UserID != int64(i+1) {
						t.Fatal("incorrect tie order", top)
					}
				}
			}
		})
	}
}

func TestTopUsesWilsonScoreBeforeRounding(t *testing.T) {
	type postVotes struct {
		owner         int64
		cringe, total int
	}
	for _, tc := range []struct {
		name        string
		posts       []postVotes
		users       []int64
		percentages []float64
	}{
		{"weighted across videos", []postVotes{{50, 1, 1}, {50, 2, 10}, {10, 1, 2}}, []int64{50, 10}, []float64{300.0 / 11, 50}},
		{"sample size can outweigh slightly higher share", []postVotes{{10, 31, 50}, {50, 23, 37}}, []int64{10, 50}, []float64{62, 2300.0 / 37}},
		{"unrounded Wilson score before vote count", []postVotes{{10, 13, 39}, {50, 1, 1}}, []int64{50, 10}, []float64{100, 100.0 / 3}},
		{"Wilson before absolute likes", []postVotes{{10, 3, 4}, {50, 2, 2}}, []int64{50, 10}, []float64{100, 75}},
		{"ties by vote count and user ID", []postVotes{{20, 0, 2}, {10, 0, 2}, {30, 0, 4}, {40, 0, 2}}, []int64{30, 10, 20, 40}, []float64{0, 0, 0, 0}},
		{"zero percent but no empty vote pool", []postVotes{{10, 0, 0}, {50, 0, 1}}, []int64{50}, []float64{0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testStore(t)
			for i, input := range tc.posts {
				p := ownedPost(t, s, i+1, "file"+string(rune('a'+i)), input.owner)
				for voter := 0; voter < input.total; voter++ {
					value := 0
					if voter < input.cringe {
						value = 1
					}
					voteFor(t, s, p, int64(1000+voter), value)
				}
			}
			top, err := s.Top(context.Background(), -100)
			if err != nil || len(top) != len(tc.users) {
				t.Fatal("incorrect eligible top", top, err)
			}
			for i, user := range tc.users {
				if top[i].UserID != user || top[i].Percentage != tc.percentages[i] {
					t.Fatal("incorrect Wilson order or displayed percentage", top, tc.users)
				}
			}
		})
	}
}

func TestUserRankingSeparatesAuthorsDeduplicatesFilesAndBreaksTies(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := ownedPost(t, s, 1, "same", 50)
	aRepeat := ownedPost(t, s, 2, "same", 50)
	b := ownedPost(t, s, 3, "same", 10)
	aOther := ownedPost(t, s, 4, "other", 50)
	voteFor(t, s, a, 999, 1)
	top, err := s.Top(ctx, -100)
	if err != nil || len(top) != 1 || top[0].UserID != 50 || top[0].Percentage != 100 {
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
	voteFor(t, s, aRepeat, 999, 1)
	voteFor(t, s, b, 999, 1)
	top, err = s.Top(ctx, -100)
	if err != nil || len(top) != 2 || top[0].UserID != 10 || top[0].Percentage != 100 || top[1].Percentage != 100 {
		t.Fatal(top, err)
	}
	voteFor(t, s, aOther, 999, 1)
	voteFor(t, s, b, 998, 0)
	top, err = s.Top(ctx, -100)
	if err != nil || len(top) != 2 || top[0].UserID != 50 || top[0].Percentage != 100 || top[1].Percentage != 50 {
		t.Fatal(top, err)
	}
	voteFor(t, s, b, 998, 1)
	top, err = s.Top(ctx, -100)
	if err != nil || len(top) != 2 || top[0].UserID != 10 || top[0].Percentage != 100 || top[1].Percentage != 100 {
		t.Fatal(top, err)
	}
	voteFor(t, s, aOther, 999, 0)
	top, err = s.Top(ctx, -100)
	if err != nil || len(top) != 2 || top[0].UserID != 10 || top[0].Percentage != 100 || top[1].Percentage != 50 {
		t.Fatal(top, err)
	}
	voteFor(t, s, aOther, 999, 1)
	top, err = s.Top(ctx, -100)
	if err != nil || len(top) != 2 || top[0].UserID != 10 {
		t.Fatal(top, err)
	}
	voteFor(t, s, b, 999, 1)
	ownedPost(t, s, 5, "same", 10)
	top, err = s.Top(ctx, -100)
	if err != nil || top[0].UserID != 10 {
		t.Fatal(top, err)
	}
	for i := 6; i <= 8; i++ {
		p := ownedPost(t, s, i, "extra"+string(rune('a'+i)), int64(i))
		voteFor(t, s, p, 999, 1)
	}
	top, err = s.Top(ctx, -100)
	if err != nil || len(top) != 5 || top[0].UserID != 10 || top[1].UserID != 50 || top[2].UserID != 6 || top[3].UserID != 7 || top[4].UserID != 8 {
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
			}(p, int64(100+i))
		}
	}
	wg.Wait()
	top, err := s.Top(ctx, -100)
	if err != nil || len(top) != 2 || top[0].Percentage != 100 || top[1].Percentage != 100 {
		t.Fatal(top, err)
	}
	first := top[0].UserID
	voteFor(t, s, a, 101, 1)
	voteFor(t, s, b, 101, 1)
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

// Seed the historical schema using its original seven-column post contract.
func legacyPost(t *testing.T, s *Store, source int, file string, owner int64) domain.Post {
	t.Helper()
	var p domain.Post
	if err := s.DB.QueryRow(`INSERT INTO media(file_unique_id) VALUES(?) ON CONFLICT(file_unique_id) DO UPDATE SET file_unique_id=excluded.file_unique_id RETURNING id`, file).Scan(&p.MediaID); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow(`INSERT INTO posts(media_id,chat_id,source_message_id,uploader_user_id,media_type) VALUES(?,-100,?,?,'video') RETURNING id`, p.MediaID, source, owner).Scan(&p.ID); err != nil {
		t.Fatal(err)
	}
	p.ChatID = -100
	p.SourceMessageID = source
	p.UploaderID = owner
	p.State = domain.Pending
	return p
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
	a := legacyPost(t, s, 1, "shared", 50)
	b := legacyPost(t, s, 2, "shared", 10)
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
	if err != nil || len(top) != 2 || top[0].UserID != 10 || top[1].UserID != 50 || top[0].Percentage != 100 || top[1].Percentage != 100 {
		t.Fatal(top, err)
	}
	var archive int
	if err = db.QueryRow(`SELECT COUNT(*) FROM votes`).Scan(&archive); err != nil || archive != 0 {
		t.Fatal(archive, err)
	}
}
func ptr(v int64) *int64 { return &v }

func TestAllVideoVotesContributeToAuthorPercentage(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := ownedPost(t, s, 1, "negative", 50)
	b := ownedPost(t, s, 2, "positive", 50)
	voteFor(t, s, a, 1, 0)
	voteFor(t, s, a, 2, 0)
	voteFor(t, s, b, 1, 1)
	var score int64
	if err := s.DB.QueryRow(`SELECT score FROM score_positions WHERE user_id=50`).Scan(&score); err != nil || score != -1 {
		t.Fatal(score, err)
	}
	top, err := s.Top(ctx, -100)
	if err != nil || len(top) != 1 || top[0].Percentage != 100.0/3 {
		t.Fatal(top, err)
	}
	voteFor(t, s, b, 2, 1)
	top, err = s.Top(ctx, -100)
	if err != nil || len(top) != 1 || top[0].Percentage != 50 {
		t.Fatal(top, err)
	}
	voteFor(t, s, b, 3, 1)
	top, err = s.Top(ctx, -100)
	if err != nil || len(top) != 1 || top[0].Percentage != 60 {
		t.Fatal(top, err)
	}
	counts, err := s.Counts(ctx, a.ID)
	if err != nil || counts.Cringe != 0 || counts.NonCringe != 2 {
		t.Fatal(counts, err)
	}
}

func TestSelfVotesRejectedForBothButtonsAndRepeatedCopies(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := ownedPost(t, s, 1, "shared", 50)
	repeat := ownedPost(t, s, 2, "shared", 50)
	other := ownedPost(t, s, 3, "shared", 10)
	for _, p := range []domain.Post{a, repeat} {
		for _, v := range []int{0, 1} {
			if _, err := s.Vote(ctx, p.ID, -100, 100+p.SourceMessageID, 50, v, 10); !errors.Is(err, domain.ErrSelfVote) {
				t.Fatal(err)
			}
		}
	}
	counts, err := s.Counts(ctx, a.ID)
	if err != nil || counts.Total() != 0 {
		t.Fatal(counts, err)
	}
	if _, err := s.Vote(ctx, a.ID, -200, 101, 50, 1, 10); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("foreign callback exposed self-vote warning", err)
	}
	voteFor(t, s, other, 50, 1)
	top, err := s.Top(ctx, -100)
	if err != nil || len(top) != 1 || top[0].UserID != 10 {
		t.Fatal(top, err)
	}
	if _, err := s.DB.Exec(`INSERT INTO author_votes(media_id,uploader_user_id,user_id,vote,updated_at) VALUES(?,50,50,1,10)`, a.MediaID); err == nil {
		t.Fatal("old deployment bypassed self-vote restriction")
	}
	voteFor(t, s, a, 999, 1)
	if _, err := s.DB.Exec(`UPDATE author_votes SET user_id=50 WHERE media_id=? AND uploader_user_id=50`, a.MediaID); err == nil {
		t.Fatal("self-vote update bypassed restriction")
	}
}

func TestNetMigrationPreservesVotesTieOrderAndManualCleanupRemovesOnlySelfVotes(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "v2.db")+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	s := &Store{DB: db}
	ctx := context.Background()
	if _, err = db.Exec(initialSchema); err != nil {
		t.Fatal(err)
	}
	for _, statement := range strings.Split(rankingSchema, "\n-- statement\n") {
		if _, err = db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	a := legacyPost(t, s, 1, "shared", 50)
	b := legacyPost(t, s, 2, "shared", 10)
	publish(t, s, a, 101)
	publish(t, s, b, 102)
	for _, v := range []struct {
		owner, voter int64
		vote         int
	}{{50, 999, 1}, {50, 50, 1}, {10, 999, 1}, {50, 998, 0}, {10, 998, 0}, {50, 997, 0}} {
		if _, err = db.Exec(`INSERT INTO author_votes(media_id,uploader_user_id,user_id,vote,updated_at) VALUES(?,?,?,?,10)`, a.MediaID, v.owner, v.voter, v.vote); err != nil {
			t.Fatal(err)
		}
	}
	var aID, bID int64
	if err = db.QueryRow(`SELECT id FROM score_positions WHERE user_id=50`).Scan(&aID); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT id FROM score_positions WHERE user_id=10`).Scan(&bID); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for _, v := range []struct{ owner, id, score int64 }{{50, aID, -1}, {10, bID, 0}} {
		var id, score int64
		if err = db.QueryRow(`SELECT id,score FROM score_positions WHERE user_id=?`, v.owner).Scan(&id, &score); err != nil || id != v.id || score != v.score {
			t.Fatal(v, id, score, err)
		}
	}
	counts, err := s.Counts(ctx, a.ID)
	if err != nil || counts.Cringe != 1 || counts.NonCringe != 2 {
		t.Fatal(counts, err)
	}
	counts, err = s.Counts(ctx, b.ID)
	if err != nil || counts.Cringe != 1 || counts.NonCringe != 1 {
		t.Fatal(counts, err)
	}
	var before int
	if err = db.QueryRow(`SELECT COUNT(*) FROM author_votes`).Scan(&before); err != nil || before != 6 {
		t.Fatal("migration deleted votes", before, err)
	}
	removed, err := s.RemoveSelfVotes(ctx)
	if err != nil || removed != 1 {
		t.Fatal(removed, err)
	}
	removed, err = s.RemoveSelfVotes(ctx)
	if err != nil || removed != 0 {
		t.Fatal(removed, err)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var votes int
	if err = db.QueryRow(`SELECT COUNT(*) FROM author_votes`).Scan(&votes); err != nil || votes != 5 {
		t.Fatal(votes, err)
	}
	if _, err = s.Vote(ctx, a.ID, -100, 101, 50, 1, 11); !errors.Is(err, domain.ErrSelfVote) {
		t.Fatal(err)
	}
	voteFor(t, s, a, 997, 1) // -1 -> +1 gives net +1.
	top, err := s.Top(ctx, -100)
	if err != nil || len(top) != 2 || top[0].UserID != 50 || top[0].Percentage != 200.0/3 || top[1].UserID != 10 || top[1].Percentage != 50 {
		t.Fatal(top, err)
	}
}
