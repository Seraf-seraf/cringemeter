package app

import (
	"context"
	"errors"
	"handler/internal/domain"
	"log/slog"
	"time"
)

const ClaimDurationSeconds int64 = 30
const MaxSyncCopies = 10
const SyncTimeout = 2 * time.Second
const EditTimeout = 700 * time.Millisecond

type Repository interface {
	Register(context.Context, domain.Incoming) (domain.Post, error)
	Claim(context.Context, int64, int64, int64) (bool, error)
	Post(context.Context, int64) (domain.Post, error)
	CopyResult(context.Context, int64, int, string, int64) error
	Deleted(context.Context, int64, int64) error
	Counts(context.Context, int64) (domain.Counts, error)
	Vote(context.Context, int64, int64, int, int64, int, int64) (int64, error)
	Copies(context.Context, int64, int64, int) ([]domain.Post, error)
	Unavailable(context.Context, int64) error
	Top(context.Context, int64) ([]domain.Rank, error)
}

type Messenger interface {
	Copy(context.Context, domain.Post, domain.Counts) (int, error)
	Delete(context.Context, int64, int) error
	Answer(context.Context, string, string, bool) error
	Edit(context.Context, domain.Post, domain.Counts) error
	SendTop(context.Context, domain.Group, []domain.Rank) error
}

type Service struct {
	Store    Repository
	Telegram Messenger
	Now      func() time.Time
}

func (s *Service) Publish(ctx context.Context, in domain.Incoming) error {
	p, err := s.Store.Register(ctx, in)
	if err != nil {
		return err
	}
	if p.State == domain.Pending || p.State == domain.Copying {
		now := s.Now().Unix()
		claimed, err := s.Store.Claim(ctx, p.ID, now, now+ClaimDurationSeconds)
		if err != nil {
			return err
		}
		if !claimed {
			p, err = s.Store.Post(ctx, p.ID)
			if err != nil {
				return err
			}
			if p.State == domain.Copying {
				return domain.ErrBusy
			}
		} else {
			counts, err := s.Store.Counts(ctx, p.ID)
			if err != nil {
				// No Telegram request was attempted, so record a confirmed failure.
				if saveErr := s.Store.CopyResult(ctx, p.ID, 0, domain.CopyFailed, s.Now().Unix()); saveErr != nil {
					return saveErr
				}
				return err
			}
			messageID, copyErr := s.Telegram.Copy(ctx, p, counts)
			state := domain.Copied
			if copyErr != nil || messageID <= 0 {
				state = domain.CopyUnknown
				var apiErr *domain.APIError
				if errors.As(copyErr, &apiErr) && apiErr.Confirmed {
					state = domain.CopyFailed
				}
				messageID = 0
			}
			if err := s.Store.CopyResult(ctx, p.ID, messageID, state, s.Now().Unix()); err != nil {
				return err
			}
			p.State, p.BotMessageID = state, messageID
			if state != domain.Copied {
				slog.WarnContext(ctx, "copy not completed", "post_id", p.ID, "phase", "copy", "code", state)
				return nil
			}
		}
	}
	if p.State == domain.Copied {
		if err := s.Telegram.Delete(ctx, p.ChatID, p.SourceMessageID); err != nil {
			var apiErr *domain.APIError
			if !errors.As(err, &apiErr) || !apiErr.MessageGone {
				return err
			}
		}
		if err := s.Store.Deleted(ctx, p.ID, s.Now().Unix()); err != nil {
			return err
		}
		p.State = domain.Done
	}
	if p.State == domain.Done {
		s.sync(ctx, p)
	}
	return nil
}

func (s *Service) RecordVote(ctx context.Context, b domain.Ballot) error {
	mediaID, err := s.Store.Vote(ctx, b.PostID, b.ChatID, b.MessageID, b.UserID, b.Vote, s.Now().Unix())
	if errors.Is(err, domain.ErrSelfVote) {
		s.answer(ctx, b.CallbackID, "Нельзя голосовать за своё видео", true)
		return nil
	}
	if errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrInvalid) {
		s.answer(ctx, b.CallbackID, "Кнопка недоступна", false)
		return nil
	}
	if err != nil {
		return err
	}
	s.answer(ctx, b.CallbackID, "Голос учтён", false)
	p := domain.Post{ID: b.PostID, MediaID: mediaID, ChatID: b.ChatID, BotMessageID: b.MessageID}
	s.sync(ctx, p)
	return nil
}

func (s *Service) answer(ctx context.Context, id, text string, showAlert bool) {
	if err := s.Telegram.Answer(ctx, id, text, showAlert); err != nil {
		slog.WarnContext(ctx, "callback answer failed", "phase", "answer", "code", "answer_failed")
	}
}

func (s *Service) sync(ctx context.Context, current domain.Post) {
	if current.BotMessageID <= 0 {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, SyncTimeout)
	defer cancel()
	counts, err := s.Store.Counts(ctx, current.ID)
	if err != nil {
		slog.WarnContext(ctx, "keyboard update deferred", "post_id", current.ID, "phase", "counts", "code", "counts_failed")
		return
	}
	if !s.edit(ctx, current, counts) {
		return
	}
	posts, err := s.Store.Copies(ctx, current.ID, 0, MaxSyncCopies)
	if err != nil {
		slog.WarnContext(ctx, "keyboard update deferred", "post_id", current.ID, "phase", "copies", "code", "copies_failed")
		return
	}
	attempts := 1
	for _, p := range posts {
		if p.ID == current.ID {
			continue
		}
		if attempts >= MaxSyncCopies || ctx.Err() != nil {
			return
		}
		attempts++
		if !s.edit(ctx, p, counts) {
			return
		}
	}
}

func (s *Service) edit(ctx context.Context, p domain.Post, counts domain.Counts) bool {
	editCtx, cancel := context.WithTimeout(ctx, EditTimeout)
	defer cancel()
	err := s.Telegram.Edit(editCtx, p, counts)
	if err == nil {
		return true
	}
	var apiErr *domain.APIError
	if errors.As(err, &apiErr) {
		if apiErr.MessageGone {
			if err := s.Store.Unavailable(ctx, p.ID); err != nil {
				slog.WarnContext(ctx, "missing copy not recorded", "post_id", p.ID, "phase", "missing_copy", "code", "store_failed")
			}
			return ctx.Err() == nil
		}
		if apiErr.NotModified {
			return true
		}
		if apiErr.Code == 429 {
			slog.WarnContext(ctx, "keyboard sync rate limited", "post_id", p.ID, "phase", "edit", "code", "rate_limited", "retry_after_seconds", apiErr.RetryAfter)
			return false
		}
	}
	slog.WarnContext(ctx, "keyboard update deferred", "post_id", p.ID, "phase", "edit", "code", "edit_failed")
	return ctx.Err() == nil
}

func (s *Service) Ranking(ctx context.Context, group domain.Group) error {
	ranks, err := s.Store.Top(ctx, group.ID)
	if err != nil {
		return err
	}
	return s.Telegram.SendTop(ctx, group, ranks)
}
