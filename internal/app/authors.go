package app

import (
	"context"
	"errors"
	"handler/internal/domain"
)

func (s *Service) ManageAuthor(ctx context.Context, a domain.AuthorAction) error {
	admin, err := s.Telegram.IsAdmin(ctx, a.ChatID, a.UserID)
	if err != nil {
		return err
	}
	if !admin {
		s.answer(ctx, a.CallbackID, "", false)
		return nil
	}
	p, err := s.Store.Post(ctx, a.PostID)
	if errors.Is(err, domain.ErrNotFound) {
		s.answer(ctx, a.CallbackID, "Пост недоступен", true)
		return nil
	}
	if err != nil {
		return err
	}
	if p.ChatID != a.ChatID || p.BotMessageID != a.MessageID || (p.State != domain.Copied && p.State != domain.Done) {
		s.answer(ctx, a.CallbackID, "Пост недоступен", true)
		return nil
	}
	if a.TargetID > 0 {
		known, err := s.Store.KnownParticipant(ctx, a.ChatID, a.TargetID)
		if err != nil {
			return err
		}
		if !known {
			s.answer(ctx, a.CallbackID, "Участник недоступен", true)
			return nil
		}
		u, err := s.Telegram.LookupParticipant(ctx, a.ChatID, a.TargetID)
		if errors.Is(err, domain.ErrInvalid) {
			s.answer(ctx, a.CallbackID, "Участник недоступен", true)
			return nil
		}
		if err != nil {
			return err
		}
		if err = s.Store.RememberParticipant(ctx, a.ChatID, u); err != nil {
			return err
		}
		p, err = s.Store.ChangeAuthor(ctx, a, s.Now().Unix())
		if errors.Is(err, domain.ErrConflict) {
			s.answer(ctx, a.CallbackID, "Не удалось сохранить автора. Откройте список заново.", true)
			return nil
		}
		if err != nil {
			return err
		}
		s.answer(ctx, a.CallbackID, "Автор изменён", false)
		s.sync(ctx, p)
		return nil
	}
	if a.Cancel {
		s.answer(ctx, a.CallbackID, "", false)
		s.sync(ctx, p)
		return nil
	}
	page, err := s.Store.Participants(ctx, a.ChatID, a.Page)
	if err != nil {
		return err
	}
	counts, err := s.Store.Counts(ctx, p.ID)
	if err != nil {
		return err
	}
	s.answer(ctx, a.CallbackID, "", false)
	err = s.Telegram.ChooseAuthor(ctx, p, counts, page)
	var apiErr *domain.APIError
	if errors.As(err, &apiErr) && apiErr.NotModified {
		return nil
	}
	return err
}
