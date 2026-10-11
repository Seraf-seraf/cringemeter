package turso

import (
	"context"
	"errors"
	"fmt"
	"handler/internal/domain"
	"sync"
	"testing"
)

func authorAction(p domain.Post, target int64) domain.AuthorAction {
	return domain.AuthorAction{AuthorClick: domain.AuthorClick{PostID: p.ID, ChatID: p.ChatID, MessageID: 100 + p.SourceMessageID, UserID: 90}, TargetID: target, ExpectedRevision: p.AuthorRevision}
}

func TestAuthorChangeMovesOneLogicalPostItsVotesAndTop(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	p := ownedPost(t, s, 1, "same", 42)
	copy := ownedPost(t, s, 2, "same", 42)
	other := ownedPost(t, s, 3, "other", 42)
	voteFor(t, s, p, 50, 1)
	voteFor(t, s, p, 51, 1)
	voteFor(t, s, p, 52, 0)
	voteFor(t, s, other, 50, 1)
	changed, err := s.ChangeAuthor(ctx, authorAction(p, 43), 10)
	if err != nil || changed.UploaderID != 43 || changed.AuthorRevision == 0 {
		t.Fatal(changed, err)
	}
	stored, _ := s.Post(ctx, copy.ID)
	if stored.UploaderID != 43 {
		t.Fatal("same logical post left with old author", stored)
	}
	stored, _ = s.Post(ctx, other.ID)
	if stored.UploaderID != 42 {
		t.Fatal("different file changed", stored)
	}
	counts, err := s.Counts(ctx, p.ID)
	if err != nil || counts.Cringe != 2 || counts.NonCringe != 1 {
		t.Fatal(counts, err)
	}
	top, err := s.Top(ctx, -100)
	if err != nil || len(top) != 2 || top[0].UserID != 43 || top[0].Score != 1 || top[1].UserID != 42 || top[1].Score != 1 {
		t.Fatal(top, err)
	}
	if _, err = s.ChangeAuthor(ctx, authorAction(p, 44), 11); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("stale selection overwrote change", err)
	}
}

func TestAuthorChangeMergesExistingPairAndRejectsNewSelfVote(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	old := ownedPost(t, s, 1, "same", 42)
	target := ownedPost(t, s, 2, "same", 43)
	voteFor(t, s, old, 50, 1)
	voteFor(t, s, old, 51, 1)
	voteFor(t, s, old, 43, 1)
	if _, err := s.Vote(ctx, target.ID, -100, 102, 50, 0, 20); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ChangeAuthor(ctx, authorAction(old, 43), 21); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ChangeAuthor(ctx, authorAction(target, 44), 22); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("stale target menu overwrote merged pool", err)
	}
	c, err := s.Counts(ctx, old.ID)
	if err != nil || c.Cringe != 1 || c.NonCringe != 1 {
		t.Fatal("duplicate voter or self-vote transferred", c, err)
	}
	other, _ := s.Counts(ctx, target.ID)
	if other != c {
		t.Fatal("same pair has separate counters", other, c)
	}
	if _, err = s.Vote(ctx, old.ID, -100, 101, 43, 1, 22); !errors.Is(err, domain.ErrSelfVote) {
		t.Fatal(err)
	}
	if _, err = s.Vote(ctx, old.ID, -100, 101, 42, 1, 23); err != nil {
		t.Fatal("former author cannot vote", err)
	}
	if top, _ := s.Top(ctx, -100); len(top) != 1 || top[0].UserID != 43 || top[0].Score != 1 {
		t.Fatal(top)
	}
}

func TestAuthorTransferPreservesTiePositionIfFinalScoreUnchanged(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	old := ownedPost(t, s, 1, "a", 42)
	target := ownedPost(t, s, 2, "b", 43)
	third := ownedPost(t, s, 3, "c", 44)
	voteFor(t, s, target, 50, 1)
	voteFor(t, s, third, 50, 1)
	voteFor(t, s, old, 51, 1)
	voteFor(t, s, old, 52, 0)
	before, _ := s.Top(ctx, -100)
	if _, err := s.ChangeAuthor(ctx, authorAction(old, 43), 20); err != nil {
		t.Fatal(err)
	}
	after, _ := s.Top(ctx, -100)
	if len(after) != 2 || before[0] != after[0] || before[1] != after[1] {
		t.Fatal(before, after)
	}
}

func TestAuthorChangeAndVotesAreAtomicAndSelectionHasOneWinner(t *testing.T) {
	s := testStore(t)
	s.DB.SetMaxOpenConns(4)
	ctx := context.Background()
	p := ownedPost(t, s, 1, "f", 42)
	var wg sync.WaitGroup
	winners := make(chan int64, 2)
	for _, owner := range []int64{43, 44} {
		wg.Add(1)
		go func(owner int64) {
			defer wg.Done()
			_, err := s.ChangeAuthor(ctx, authorAction(p, owner), 20)
			if err == nil {
				winners <- owner
			} else if !errors.Is(err, domain.ErrConflict) {
				t.Error(err)
			}
		}(owner)
	}
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := s.Vote(ctx, p.ID, -100, 101, int64(100+i), 1, 20); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	close(winners)
	var winner int64
	n := 0
	for id := range winners {
		winner = id
		n++
	}
	if n != 1 {
		t.Fatal("selection winners", n)
	}
	c, err := s.Counts(ctx, p.ID)
	if err != nil || c.Cringe != 30 {
		t.Fatal(c, err)
	}
	top, err := s.Top(ctx, -100)
	if err != nil || len(top) != 1 || top[0].UserID != winner || top[0].Score != 30 {
		t.Fatal(top, err)
	}
}

func TestAuthorChangeRollbackAndForeignChatDoNotAlterVotes(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	p := ownedPost(t, s, 1, "f", 42)
	voteFor(t, s, p, 50, 1)
	if _, err := s.DB.Exec(`CREATE TRIGGER fail_author BEFORE INSERT ON author_changes BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ChangeAuthor(ctx, authorAction(p, 43), 20); err == nil {
		t.Fatal("failed transaction committed")
	}
	stored, _ := s.Post(ctx, p.ID)
	c, _ := s.Counts(ctx, p.ID)
	if stored.UploaderID != 42 || stored.AuthorRevision != 0 || c.Cringe != 1 {
		t.Fatal(stored, c)
	}
	if _, err := s.DB.Exec(`DROP TRIGGER fail_author`); err != nil {
		t.Fatal(err)
	}
	a := authorAction(p, 43)
	a.ChatID = -200
	if _, err := s.ChangeAuthor(ctx, a, 20); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("foreign chat changed", err)
	}
}

func TestKnownParticipantsExcludeBotsAndPaginateWithinChat(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for i := int64(1); i <= 12; i++ {
		if err := s.RememberParticipant(ctx, -100, domain.Participant{ID: i, Username: "known"}); err != nil {
			t.Fatal(err)
		}
	}
	s.RememberParticipant(ctx, -200, domain.Participant{ID: 99})
	s.RegisterDownloader(ctx, 12)
	page, err := s.Participants(ctx, -100, 0)
	if err != nil || len(page.Users) != 10 || page.Index != 0 || page.Pages != 2 {
		t.Fatal(page, err)
	}
	for i, u := range page.Users {
		if u.ID != int64(i+1) {
			t.Fatal("skipped or repeated participant", page)
		}
	}
	page, err = s.Participants(ctx, -100, 1)
	if err != nil || len(page.Users) != 1 || page.Users[0].ID != 11 || page.Index != 1 || page.Pages != 2 {
		t.Fatal(page, err)
	}
	page, err = s.Participants(ctx, -100, 2)
	if err != nil || page.Index != 1 || len(page.Users) != 1 || page.Users[0].ID != 11 {
		t.Fatal("page did not clamp", page, err)
	}
	for _, id := range []int64{12, 99} {
		known, err := s.KnownParticipant(ctx, -100, id)
		if err != nil || known {
			t.Fatal(id, known, err)
		}
	}
}

func TestAuthorManagementUpgradePreservesExistingVotesAndQueue(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	p := ownedPost(t, s, 1, "same", 42)
	voteFor(t, s, p, 50, 1)
	if err := s.RememberRequest(ctx, domain.VideoRequest{ChatID: -100, MessageID: 5, UserID: 42, VideoIDs: []string{"QisvmE7zRY0"}, SentAt: 10}); err != nil {
		t.Fatal(err)
	}
	var oldPosition int64
	if err := s.DB.QueryRow(`SELECT id FROM score_positions WHERE chat_id=-100 AND user_id=42`).Scan(&oldPosition); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`DROP TABLE author_changes; DROP TABLE participants`); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Check(ctx); err != nil {
		t.Fatal(err)
	}
	c, err := s.Counts(ctx, p.ID)
	if err != nil || c.Cringe != 1 {
		t.Fatal(c, err)
	}
	var position int64
	var pending int
	if err := s.DB.QueryRow(`SELECT id FROM score_positions WHERE chat_id=-100 AND user_id=42`).Scan(&position); err != nil || position != oldPosition {
		t.Fatal(position, oldPosition, err)
	}
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM source_requests`).Scan(&pending); err != nil || pending != 1 {
		t.Fatal(pending, err)
	}
	if _, err := s.ChangeAuthor(ctx, authorAction(p, 43), 20); err != nil {
		t.Fatal(err)
	}
}

func TestParticipantPaginationEmptyAndTenUserBoundaries(t *testing.T) {
	for _, n := range []int{0, 9, 10, 11, 21} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			s := testStore(t)
			ctx := context.Background()
			for i := 1; i <= n; i++ {
				if err := s.RememberParticipant(ctx, -100, domain.Participant{ID: int64(i)}); err != nil {
					t.Fatal(err)
				}
			}
			pages := (n + 9) / 10
			if pages == 0 {
				pages = 1
			}
			var ids []int64
			for index := 0; index < pages; index++ {
				p, err := s.Participants(ctx, -100, index)
				if err != nil || p.Index != index || p.Pages != pages || len(p.Users) > 10 {
					t.Fatal(p, err)
				}
				for _, u := range p.Users {
					ids = append(ids, u.ID)
				}
			}
			if len(ids) != n {
				t.Fatal("missing participants", ids, n)
			}
			for i, id := range ids {
				if id != int64(i+1) {
					t.Fatal("participant repeated or skipped", ids)
				}
			}
			last, err := s.Participants(ctx, -100, 100000)
			if err != nil || last.Index != pages-1 || last.Pages != pages {
				t.Fatal(last, err)
			}
		})
	}
}
