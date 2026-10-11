package turso

import (
	"context"
	"handler/internal/domain"
)

type UploadDiagnostic struct {
	PostID          int64  `json:"post_id"`
	MediaID         int64  `json:"media_id"`
	SourceMessageID int    `json:"source_message_id"`
	BotMessageID    int    `json:"bot_message_id"`
	UploaderID      int64  `json:"uploader_user_id"`
	MediaType       string `json:"media_type"`
	State           string `json:"state"`
	CreatedAt       int64  `json:"created_at"`
	Cringe          int64  `json:"cringe_votes"`
	NonCringe       int64  `json:"non_cringe_votes"`
}

// Fixed, read-only, chat-scoped report. No caller-supplied SQL or media content.
func (s *Store) RecentUploads(ctx context.Context, chatID int64) ([]UploadDiagnostic, error) {
	if chatID >= 0 {
		return nil, domain.ErrInvalid
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT p.id,p.media_id,p.source_message_id,COALESCE(p.bot_message_id,0),COALESCE(p.uploader_user_id,0),p.media_type,p.state,p.created_at,
 (SELECT COUNT(*) FROM author_votes v WHERE v.media_id=p.media_id AND v.uploader_user_id=p.uploader_user_id AND v.user_id!=v.uploader_user_id AND v.vote=1),
 (SELECT COUNT(*) FROM author_votes v WHERE v.media_id=p.media_id AND v.uploader_user_id=p.uploader_user_id AND v.user_id!=v.uploader_user_id AND v.vote=0)
 FROM posts p WHERE p.chat_id=? ORDER BY p.id DESC LIMIT 20`, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	report := make([]UploadDiagnostic, 0)
	for rows.Next() {
		var p UploadDiagnostic
		if err := rows.Scan(&p.PostID, &p.MediaID, &p.SourceMessageID, &p.BotMessageID, &p.UploaderID, &p.MediaType, &p.State, &p.CreatedAt, &p.Cringe, &p.NonCringe); err != nil {
			return nil, err
		}
		report = append(report, p)
	}
	return report, rows.Err()
}
