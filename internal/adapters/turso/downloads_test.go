package turso

import (
	"context"
	"errors"
	"handler/internal/domain"
	"sync"
	"testing"
	"time"
)

func enqueue(t *testing.T, s *Store, msg int, user int64, ids ...string) domain.VideoRequest {
	t.Helper()
	r := domain.VideoRequest{ChatID: -100, MessageID: msg, UserID: user, VideoIDs: ids, SentAt: s.queueNow()}
	if err := s.RememberRequest(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	return r
}
func downloaded(msg int, file string) domain.Incoming {
	return domain.Incoming{ChatID: -100, MessageID: msg, MediaType: "video", UniqueID: file, SenderBotID: 99, AllowFIFO: true}
}

func TestDownloadFIFOExpiresAtFiveMinutes(t *testing.T) {
	for _, age := range []int64{299, 300, 301} {
		t.Run((time.Duration(age) * time.Second).String(), func(t *testing.T) {
			s := testStore(t)
			ctx := context.Background()
			s.now = func() time.Time { return time.Unix(1000-age, 0) }
			r := enqueue(t, s, 1, 42, "abcdefghijk")
			s.now = func() time.Time { return time.Unix(1000, 0) }
			visible, err := s.PendingRequests(ctx, -100)
			if err != nil || (len(visible) == 1) != (age < 300) {
				t.Fatal("expired request is visible", visible, err)
			}
			p, err := s.Register(ctx, downloaded(2, "f"))
			if err != nil || (p.UploaderID == 42) != (age < 300) {
				t.Fatal("FIFO expiry boundary", age, p, err)
			}
			if err = s.RememberRequest(ctx, r); err != nil {
				t.Fatal(err)
			}
			var remaining int
			if err = s.DB.QueryRow(`SELECT COUNT(*) FROM download_requests`).Scan(&remaining); err != nil || (remaining == 1) != (age < 300) {
				t.Fatal("expired request resurrected", remaining, err)
			}
		})
	}
}

func TestDownloadCleanupPreservesReservedRequestAndHistory(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	enqueue(t, s, 1, 42, "abcdefghijk")
	enqueue(t, s, 2, 43, "ZYXWVUTSRQP")
	in := downloaded(3, "f")
	p, err := s.Register(ctx, in)
	if err != nil || p.UploaderID != 42 {
		t.Fatal(p, err)
	}
	s.now = func() time.Time { return time.Unix(310, 0) }
	enqueue(t, s, 4, 44, "aaaaaaaaaaa")
	q, err := s.PendingRequests(ctx, -100)
	if err != nil || len(q) != 2 || q[0].PostID != p.ID || q[1].UserID != 44 {
		t.Fatal("cleanup deleted reservation or kept expired request", q, err)
	}
	again, err := s.Register(ctx, in)
	if err != nil || again.ID != p.ID || again.UploaderID != 42 {
		t.Fatal("expiry changed reserved author on replay", again, err)
	}
	next, err := s.Register(ctx, downloaded(5, "g"))
	if err != nil || next.UploaderID != 44 {
		t.Fatal("expired request stole new download", next, err)
	}
	publish(t, s, p, 101)
	if err = s.Deleted(ctx, p.ID, 311); err != nil {
		t.Fatal(err)
	}
	rows, err := s.RecentUploads(ctx, -100)
	if err != nil || len(rows) != 2 || rows[1].Evidence != "fifo" || rows[1].RequestMessageID != 1 || rows[1].UploaderID != 42 {
		t.Fatal("cleanup removed attribution history", rows, err)
	}
}

func TestDownloadExpiredReplyStillProvesSource(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	r := enqueue(t, s, 1, 42, "abcdefghijk")
	s.now = func() time.Time { return time.Unix(310, 0) }
	in := downloaded(2, "f")
	in.SourceRequest = &r
	p, err := s.Register(ctx, in)
	if err != nil || p.UploaderID != 42 {
		t.Fatal("FIFO timeout rejected explicit reply", p, err)
	}
	rows, err := s.RecentUploads(ctx, -100)
	if err != nil || len(rows) != 1 || rows[0].Evidence != "reply" || !rows[0].Verified {
		t.Fatal(rows, err)
	}
}

func TestConcurrentDownloadExpiryAndReplayDoNotStealAuthor(t *testing.T) {
	s := testStore(t)
	s.DB.SetMaxOpenConns(4)
	ctx := context.Background()
	old := enqueue(t, s, 1, 42, "abcdefghijk")
	s.now = func() time.Time { return time.Unix(309, 0) }
	enqueue(t, s, 2, 43, "ZYXWVUTSRQP")
	s.now = func() time.Time { return time.Unix(310, 0) }
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				p, err := s.Register(ctx, downloaded(3, "f"))
				if err != nil || p.UploaderID != 43 {
					t.Error("expired request stole author", p, err)
				}
			} else if err := s.RememberRequest(ctx, old); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	var requests, posts, attrs int
	if err := s.DB.QueryRow(`SELECT (SELECT COUNT(*) FROM download_requests),(SELECT COUNT(*) FROM posts),(SELECT COUNT(*) FROM download_attributions)`).Scan(&requests, &posts, &attrs); err != nil || requests != 1 || posts != 1 || attrs != 1 {
		t.Fatal("cleanup/replay duplicated or resurrected request", requests, posts, attrs, err)
	}
}

func TestDownloadFIFOReservationReplayCompletionAndEvidence(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	r := enqueue(t, s, 1, 42, "abcdefghijk")
	enqueue(t, s, 2, 43, "ZYXWVUTSRQP")
	in := downloaded(3, "f")
	p, err := s.Register(ctx, in)
	if err != nil || p.UploaderID != 42 {
		t.Fatal(p, err)
	}
	again, err := s.Register(ctx, in)
	if err != nil || again.ID != p.ID {
		t.Fatal(again, err)
	}
	q, err := s.PendingRequests(ctx, -100)
	if err != nil || len(q) != 2 || q[0].PostID != p.ID || q[1].PostID != 0 {
		t.Fatal(q, err)
	}
	if err = s.Deleted(ctx, p.ID, 2); !errors.Is(err, domain.ErrConflict) {
		t.Fatal(err)
	}
	q, _ = s.PendingRequests(ctx, -100)
	if len(q) != 2 {
		t.Fatal("invalid lifecycle popped request", q)
	}
	publish(t, s, p, 101)
	if _, err = s.Vote(ctx, p.ID, -100, 101, 42, 1, 3); !errors.Is(err, domain.ErrSelfVote) {
		t.Fatal("initiating user can self-vote", err)
	}
	if _, err = s.Vote(ctx, p.ID, -100, 101, 50, 1, 3); err != nil {
		t.Fatal(err)
	}
	if err = s.Deleted(ctx, p.ID, 3); err != nil {
		t.Fatal(err)
	}
	if err = s.RememberRequest(ctx, r); err != nil {
		t.Fatal(err)
	}
	q, _ = s.PendingRequests(ctx, -100)
	if len(q) != 1 || q[0].UserID != 43 {
		t.Fatal("processed request resurrected", q)
	}
	rows, err := s.RecentUploads(ctx, -100)
	if err != nil || len(rows) != 1 || rows[0].Evidence != "fifo" || rows[0].Verified || rows[0].RequestMessageID != 1 || rows[0].YouTubeID != "abcdefghijk" || rows[0].ObservedYouTubeID != "" {
		t.Fatal(rows, err)
	}
	second, err := s.Register(ctx, downloaded(4, "g"))
	if err != nil || second.UploaderID != 43 {
		t.Fatal(second, err)
	}
}

func TestFailedAttributionRollsBackPublicationAndQueueReservation(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	enqueue(t, s, 1, 42, "abcdefghijk")
	if _, err := s.DB.Exec(`CREATE TRIGGER fail_attribution BEFORE INSERT ON download_attributions BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Register(ctx, downloaded(2, "f")); err == nil {
		t.Fatal("failure swallowed")
	}
	q, err := s.PendingRequests(ctx, -100)
	if err != nil || len(q) != 1 || q[0].PostID != 0 {
		t.Fatal(q, err)
	}
	var posts, media, bots int
	if err = s.DB.QueryRow(`SELECT (SELECT COUNT(*) FROM posts),(SELECT COUNT(*) FROM media),(SELECT COUNT(*) FROM bot_senders)`).Scan(&posts, &media, &bots); err != nil || posts != 0 || media != 0 || bots != 0 {
		t.Fatal(posts, media, bots, err)
	}
	if _, err = s.DB.Exec(`DROP TRIGGER fail_attribution`); err != nil {
		t.Fatal(err)
	}
	p, err := s.Register(ctx, downloaded(2, "f"))
	if err != nil || p.UploaderID != 42 {
		t.Fatal(p, err)
	}
}

func TestVerifiedSourceOverridesFIFOAndMismatchStaysUnknown(t *testing.T) {
	for _, tc := range []struct {
		name     string
		reply    bool
		observed string
		expected int64
		evidence string
	}{
		{"metadata out of order", false, "ZYXWVUTSRQP", 43, "youtube_url"},
		{"direct reply", true, "", 43, "reply"},
		{"reply metadata disagreement", true, "abcdefghijk", 0, ""},
		{"different video", false, "aaaaaaaaaaa", 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testStore(t)
			ctx := context.Background()
			enqueue(t, s, 1, 42, "abcdefghijk")
			r := enqueue(t, s, 2, 43, "ZYXWVUTSRQP")
			in := downloaded(3, "f")
			if tc.reply {
				in.SourceRequest = &r
			}
			if tc.observed != "" {
				in.SourceVideoIDs = []string{tc.observed}
				in.SourceEvidence = "youtube_url"
			}
			p, err := s.Register(ctx, in)
			if err != nil || p.UploaderID != tc.expected {
				t.Fatal(p, err)
			}
			rows, err := s.RecentUploads(ctx, -100)
			if err != nil || rows[0].Evidence != tc.evidence || rows[0].Verified != (tc.evidence != "") {
				t.Fatal(rows, err)
			}
		})
	}
}

func TestAmbiguousUntrustedEmptyAndFutureRequestsDoNotAttribute(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	enqueue(t, s, 1, 42, "abcdefghijk")
	enqueue(t, s, 2, 43, "abcdefghijk")
	in := downloaded(3, "f")
	in.SourceVideoIDs = []string{"abcdefghijk"}
	in.SourceEvidence = "youtube_url"
	p, err := s.Register(ctx, in)
	if err != nil || p.UploaderID != 0 {
		t.Fatal(p, err)
	}
	in = downloaded(4, "g")
	in.AllowFIFO = false
	p, err = s.Register(ctx, in)
	if err != nil || p.UploaderID != 0 {
		t.Fatal(p, err)
	}
	// A video cannot consume a later link even when that webhook arrived first.
	future := testStore(t)
	enqueue(t, future, 10, 42, "abcdefghijk")
	p, err = future.Register(ctx, downloaded(9, "f"))
	if err != nil || p.UploaderID != 0 {
		t.Fatal(p, err)
	}
	empty := testStore(t)
	p, err = empty.Register(ctx, downloaded(1, "f"))
	if err != nil || p.UploaderID != 0 {
		t.Fatal(p, err)
	}
	publish(t, empty, p, 101)
	if _, err = empty.Vote(ctx, p.ID, -100, 101, 50, 1, 3); err != nil {
		t.Fatal(err)
	}
	top, err := empty.Top(ctx, -100)
	if err != nil || len(top) != 0 {
		t.Fatal(top, err)
	}
}

func TestConcurrentDownloadReservationsDoNotReuseRequests(t *testing.T) {
	s := testStore(t)
	s.DB.SetMaxOpenConns(4)
	ctx := context.Background()
	enqueue(t, s, 1, 42, "abcdefghijk")
	enqueue(t, s, 2, 43, "ZYXWVUTSRQP")
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := s.Register(ctx, downloaded(3+i%2, "f"))
			if err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	var posts, attrs, owners int
	s.DB.QueryRow(`SELECT COUNT(*),COUNT(DISTINCT user_id) FROM download_attributions`).Scan(&attrs, &owners)
	s.DB.QueryRow(`SELECT COUNT(*) FROM posts`).Scan(&posts)
	if posts != 2 || attrs != 2 || owners != 2 {
		t.Fatal(posts, attrs, owners)
	}
}

func TestDownloadMigrationPreservesNetVotesAndTieOrderAndExcludesBots(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	human := ownedPost(t, s, 1, "h", 42)
	bot := ownedPost(t, s, 2, "b", 99)
	for _, p := range []domain.Post{human, bot} {
		if _, err := s.Vote(ctx, p.ID, -100, 100+p.SourceMessageID, 50, 1, 3); err != nil {
			t.Fatal(err)
		}
	}
	// Drop only additive objects to recreate the exact previous schema.
	before, _ := s.Top(ctx, -100)
	var position int64
	s.DB.QueryRow(`SELECT id FROM score_positions WHERE user_id=42`).Scan(&position)
	for _, name := range []string{"download_attributions", "download_requests", "bot_senders"} {
		if _, err := s.DB.Exec("DROP TABLE " + name); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	after, _ := s.Top(ctx, -100)
	if len(before) != 2 || len(after) != 2 || before[0] != after[0] || before[1] != after[1] {
		t.Fatal(before, after)
	}
	var current int64
	s.DB.QueryRow(`SELECT id FROM score_positions WHERE user_id=42`).Scan(&current)
	if position != current {
		t.Fatal("tie position changed")
	}
	if err := s.RegisterDownloader(ctx, 99); err != nil {
		t.Fatal(err)
	}
	after, _ = s.Top(ctx, -100)
	if len(after) != 1 || after[0].UserID != 42 {
		t.Fatal(after)
	}
	var count int
	s.DB.QueryRow(`SELECT COUNT(*) FROM author_votes`).Scan(&count)
	if count != 2 {
		t.Fatal("votes removed", count)
	}
}
