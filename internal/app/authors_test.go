package app

import (
	"context"
	"errors"
	"handler/internal/domain"
	"testing"
)

func managedPost(t *testing.T, s *Service, owner int64) domain.AuthorAction {
	t.Helper()
	in := input(1)
	in.UploaderID = &owner
	if err := s.Publish(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if fake, ok := s.Telegram.(*fakeTelegram); ok {
		fake.edits = nil
		fake.counts = nil
	}
	return domain.AuthorAction{AuthorClick: domain.AuthorClick{PostID: 1, ChatID: -100, MessageID: 101, UserID: 90, CallbackID: "author"}}
}

func TestNonAdministratorCannotOpenSelectOrCloseAuthor(t *testing.T) {
	for _, mode := range []string{"open", "select", "close"} {
		t.Run(mode, func(t *testing.T) {
			s, store, tg := setup(t)
			ctx := context.Background()
			a := managedPost(t, s, 42)
			if mode == "select" {
				a.TargetID = 43
			}
			if mode == "close" {
				a.Cancel = true
			}
			if err := s.ManageAuthor(ctx, a); err != nil {
				t.Fatal(err)
			}
			p, _ := store.Post(ctx, 1)
			if p.UploaderID != 42 || p.AuthorRevision != 0 || tg.choices != 0 || len(tg.edits) != 0 || tg.answers != 1 || tg.answerText != "" || tg.answerAlert {
				t.Fatal(p, tg)
			}
		})
	}
}

func TestAdministratorChoosesKnownHumanAndMovesVotesOnAllCopies(t *testing.T) {
	s, store, tg := setup(t)
	ctx := context.Background()
	a := managedPost(t, s, 42)
	tg.admin = true
	if err := store.RememberParticipant(ctx, -100, domain.Participant{ID: 43, Username: "new_owner"}); err != nil {
		t.Fatal(err)
	}
	in := input(2)
	owner := int64(42)
	in.UploaderID = &owner
	if err := s.Publish(ctx, in); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Vote(ctx, 1, -100, 101, 50, 1, 10); err != nil {
		t.Fatal(err)
	}
	if err := s.ManageAuthor(ctx, a); err != nil {
		t.Fatal(err)
	}
	if tg.choices != 1 || len(tg.selectedUsers) != 3 {
		t.Fatal(tg.selectedUsers, tg.choices)
	}
	a.TargetID = 43
	tg.edits = nil
	tg.counts = nil
	if err := s.ManageAuthor(ctx, a); err != nil {
		t.Fatal(err)
	}
	top, err := store.Top(ctx, -100)
	if err != nil || len(top) != 1 || top[0].UserID != 43 || top[0].Percentage != 100 {
		t.Fatal(top, err)
	}
	if len(tg.edits) != 2 {
		t.Fatal(tg.edits)
	}
	for _, p := range tg.edits {
		if p.UploaderID != 43 || p.AuthorUsername != "selected" {
			t.Fatal(p)
		}
	}
	if err := s.RecordVote(ctx, domain.Ballot{PostID: 1, ChatID: -100, MessageID: 101, UserID: 43, Vote: 1, CallbackID: "self"}); err != nil {
		t.Fatal(err)
	}
	if !tg.answerAlert || tg.answerText != "Нельзя голосовать за своё видео" {
		t.Fatal(tg.answerText, tg.answerAlert)
	}
}

func TestAuthorSelectionRejectsUnknownForeignAndStaleCallbacks(t *testing.T) {
	for _, mode := range []string{"unknown", "foreign_chat", "foreign_message", "stale"} {
		t.Run(mode, func(t *testing.T) {
			s, store, tg := setup(t)
			ctx := context.Background()
			a := managedPost(t, s, 42)
			tg.admin = true
			a.TargetID = 43
			if mode != "unknown" {
				if err := store.RememberParticipant(ctx, -100, domain.Participant{ID: 43}); err != nil {
					t.Fatal(err)
				}
			}
			switch mode {
			case "foreign_chat":
				a.ChatID = -200
			case "foreign_message":
				a.MessageID = 999
			case "stale":
				a.ExpectedRevision = 123
			}
			if err := s.ManageAuthor(ctx, a); err != nil {
				t.Fatal(err)
			}
			p, _ := store.Post(ctx, 1)
			if p.UploaderID != 42 || p.AuthorRevision != 0 || len(tg.edits) != 0 || !tg.answerAlert {
				t.Fatal(p, tg)
			}
		})
	}
}

func TestAuthorPermissionFailureDoesNotAlterPost(t *testing.T) {
	s, store, tg := setup(t)
	a := managedPost(t, s, 42)
	a.TargetID = 43
	tg.adminErr = errors.New("permission lookup unavailable")
	if err := s.ManageAuthor(context.Background(), a); !errors.Is(err, tg.adminErr) {
		t.Fatal(err)
	}
	p, _ := store.Post(context.Background(), 1)
	if p.UploaderID != 42 || p.AuthorRevision != 0 || tg.answers != 0 || tg.choices != 0 || len(tg.edits) != 0 {
		t.Fatal(p, tg)
	}
}

func TestAdministratorCannotAssignKnownButInvalidGroupParticipant(t *testing.T) {
	s, store, tg := setup(t)
	ctx := context.Background()
	a := managedPost(t, s, 42)
	tg.admin = true
	a.TargetID = 43
	if err := store.RememberParticipant(ctx, -100, domain.Participant{ID: 43, Username: "cached"}); err != nil {
		t.Fatal(err)
	}
	// The real adapter returns ErrInvalid for a bot, a departed participant,
	// or a getChatMember response that belongs to a different user.
	tg.lookupErr = domain.ErrInvalid
	if err := s.ManageAuthor(ctx, a); err != nil {
		t.Fatal(err)
	}
	p, _ := store.Post(ctx, 1)
	if p.UploaderID != 42 || p.AuthorRevision != 0 || len(tg.edits) != 0 || tg.answerText != "Участник недоступен" || !tg.answerAlert {
		t.Fatal(p, tg)
	}
}
