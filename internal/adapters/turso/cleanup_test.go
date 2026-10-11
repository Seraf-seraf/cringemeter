package turso

import (
	"context"
	"handler/internal/domain"
	"reflect"
	"testing"
)

func TestBotOwnedVotesCleanupPreservesHumanPoolsAndPosts(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	bot := ownedPost(t, s, 1, "shared", 99)
	human := ownedPost(t, s, 2, "shared", 42)
	copy := ownedPost(t, s, 3, "shared", 99)
	voteFor(t, s, bot, 50, 1)
	voteFor(t, s, copy, 51, 0)
	voteFor(t, s, human, 50, 1)
	// A bot as voter on a human pool is not the owner being removed.
	voteFor(t, s, human, 99, 0)
	if err := s.RegisterDownloader(ctx, 99); err != nil {
		t.Fatal(err)
	}
	if err := s.RememberRequest(ctx, domain.VideoRequest{ChatID: -100, MessageID: 5, UserID: 42, VideoIDs: []string{"QisvmE7zRY0"}, SentAt: 10}); err != nil {
		t.Fatal(err)
	}
	topBefore, _ := s.Top(ctx, -100)
	humanBefore, _ := s.Counts(ctx, human.ID)
	r, err := s.RemoveBotVotes(ctx)
	if err != nil || r.Removed != 2 || r.Remaining != 0 || r.HumanBefore != 2 || r.HumanAfter != 2 {
		t.Fatal(r, err)
	}
	c, _ := s.Counts(ctx, bot.ID)
	h, _ := s.Counts(ctx, human.ID)
	topAfter, _ := s.Top(ctx, -100)
	if c.Total() != 0 || h != humanBefore || !reflect.DeepEqual(topBefore, topAfter) {
		t.Fatal(c, h, humanBefore, topBefore, topAfter)
	}
	if known, err := s.KnownParticipant(ctx, -100, 51); err != nil || !known {
		t.Fatal("cleanup lost a known human voter", known, err)
	}
	var posts, queue int
	s.DB.QueryRow(`SELECT COUNT(*) FROM posts`).Scan(&posts)
	s.DB.QueryRow(`SELECT COUNT(*) FROM download_requests`).Scan(&queue)
	if posts != 3 || queue != 1 {
		t.Fatal(posts, queue)
	}
	r, err = s.RemoveBotVotes(ctx)
	if err != nil || r.Removed != 0 || r.Remaining != 0 || r.HumanAfter != 2 {
		t.Fatal(r, err)
	}
}

func TestBotVoteCleanupKeepsVotesAlreadyTransferredToHuman(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	p := ownedPost(t, s, 1, "f", 99)
	voteFor(t, s, p, 50, 1)
	s.RegisterDownloader(ctx, 99)
	if _, err := s.ChangeAuthor(ctx, authorAction(p, 42), 20); err != nil {
		t.Fatal(err)
	}
	before, _ := s.Top(ctx, -100)
	r, err := s.RemoveBotVotes(ctx)
	after, _ := s.Top(ctx, -100)
	c, _ := s.Counts(ctx, p.ID)
	if err != nil || r.Removed != 0 || r.HumanAfter != 1 || c.Cringe != 1 || !reflect.DeepEqual(before, after) {
		t.Fatal(r, c, before, after, err)
	}
}

func TestBotVoteCleanupRollsBackIfMetadataUpdateFails(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	p := ownedPost(t, s, 1, "f", 99)
	voteFor(t, s, p, 50, 1)
	s.RegisterDownloader(ctx, 99)
	if _, err := s.DB.Exec(`CREATE TRIGGER fail_cleanup BEFORE UPDATE ON score_positions WHEN OLD.user_id=99 BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RemoveBotVotes(ctx); err == nil {
		t.Fatal("cleanup failure ignored")
	}
	c, _ := s.Counts(ctx, p.ID)
	if c.Cringe != 1 {
		t.Fatal("votes partially deleted", c)
	}
}
