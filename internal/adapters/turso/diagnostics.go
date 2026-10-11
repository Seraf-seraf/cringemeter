package turso

import (
	"context"
	"handler/internal/domain"
)

type UploadDiagnostic struct {
	PostID            int64  `json:"post_id"`
	MediaID           int64  `json:"media_id"`
	SourceMessageID   int    `json:"source_message_id"`
	BotMessageID      int    `json:"bot_message_id"`
	UploaderID        int64  `json:"uploader_user_id"`
	MediaType         string `json:"media_type"`
	State             string `json:"state"`
	CreatedAt         int64  `json:"created_at"`
	Cringe            int64  `json:"cringe_votes"`
	NonCringe         int64  `json:"non_cringe_votes"`
	BotUploader       bool   `json:"bot_uploader"`
	RequestMessageID  int    `json:"request_message_id"`
	YouTubeID         string `json:"youtube_video_id"`
	ObservedYouTubeID string `json:"observed_youtube_video_id"`
	SourceID          string `json:"source_video_id"`
	ObservedSourceID  string `json:"observed_source_video_id"`
	Evidence          string `json:"evidence"`
	Verified          bool   `json:"verified"`
}

// Fixed, read-only, chat-scoped report. No caller-supplied SQL or media content.
func (s *Store) RecentUploads(ctx context.Context, chatID int64) ([]UploadDiagnostic, error) {
	if chatID >= 0 {
		return nil, domain.ErrInvalid
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT p.id,p.media_id,p.source_message_id,COALESCE(p.bot_message_id,0),COALESCE(p.uploader_user_id,0),p.media_type,p.state,p.created_at,
 (SELECT COUNT(*) FROM author_votes v WHERE v.media_id=p.media_id AND v.uploader_user_id=COALESCE(p.uploader_user_id,0) AND v.user_id!=v.uploader_user_id AND v.vote=1),
 (SELECT COUNT(*) FROM author_votes v WHERE v.media_id=p.media_id AND v.uploader_user_id=COALESCE(p.uploader_user_id,0) AND v.user_id!=v.uploader_user_id AND v.vote=0),
 EXISTS(SELECT 1 FROM bot_senders b WHERE b.user_id=p.uploader_user_id),
 COALESCE(a.source_message_id,0),COALESCE(a.source_video_id,''),COALESCE(a.observed_source_video_id,''),COALESCE(a.evidence,''),COALESCE(a.verified,0)
 FROM posts p LEFT JOIN source_attributions a ON a.post_id=p.id WHERE p.chat_id=? ORDER BY p.id DESC LIMIT 20`, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	report := make([]UploadDiagnostic, 0)
	for rows.Next() {
		var p UploadDiagnostic
		if err := rows.Scan(&p.PostID, &p.MediaID, &p.SourceMessageID, &p.BotMessageID, &p.UploaderID, &p.MediaType, &p.State, &p.CreatedAt, &p.Cringe, &p.NonCringe, &p.BotUploader, &p.RequestMessageID, &p.SourceID, &p.ObservedSourceID, &p.Evidence, &p.Verified); err != nil {
			return nil, err
		}
		if domain.ValidYouTubeID(p.SourceID) {
			p.YouTubeID = p.SourceID
		}
		if domain.ValidYouTubeID(p.ObservedSourceID) {
			p.ObservedYouTubeID = p.ObservedSourceID
		}
		report = append(report, p)
	}
	return report, rows.Err()
}

type RequestDiagnostic struct {
	MessageID int    `json:"message_id"`
	YouTubeID string `json:"youtube_video_id"`
	SourceID  string `json:"source_video_id"`
	UserID    int64  `json:"user_id"`
	PostID    int64  `json:"reserved_post_id"`
}

func (s *Store) PendingRequests(ctx context.Context, chatID int64) ([]RequestDiagnostic, error) {
	if chatID >= 0 {
		return nil, domain.ErrInvalid
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT source_message_id,source_video_id,user_id,COALESCE(post_id,0) FROM source_requests WHERE chat_id=? AND (post_id IS NOT NULL OR sent_at>?) ORDER BY source_message_id,source_video_id LIMIT 20`, chatID, s.queueNow()-domain.DownloadRequestTTLSeconds)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]RequestDiagnostic, 0)
	for rows.Next() {
		var r RequestDiagnostic
		if err = rows.Scan(&r.MessageID, &r.SourceID, &r.UserID, &r.PostID); err != nil {
			return nil, err
		}
		if domain.ValidYouTubeID(r.SourceID) {
			r.YouTubeID = r.SourceID
		}
		result = append(result, r)
	}
	return result, rows.Err()
}
