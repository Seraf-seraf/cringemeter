package app

import (
	"context"
	"errors"
	"handler/internal/domain"
	"log/slog"
	"time"
)

const ClaimDurationSeconds int64 = 30

type Repository interface {
	Register(context.Context, domain.Incoming) (domain.Post, error)
	Claim(context.Context, int64, int64, int64) (bool, error)
	Post(context.Context, int64) (domain.Post, error)
	CopyResult(context.Context, int64, int, string, int64) error
	Deleted(context.Context, int64, int64) error
	Counts(context.Context, int64) (domain.Counts, error)
}

type Messenger interface {
	Copy(context.Context, domain.Post, domain.Counts) (int, error)
	Delete(context.Context, int64, int) error
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
			counts, err := s.Store.Counts(ctx, p.MediaID)
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
		return s.Store.Deleted(ctx, p.ID, s.Now().Unix())
	}
	return nil
}
