package turso

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"handler/internal/domain"
)

// BEGIN must be the first operation on a remote libsql stream. Keep the
// publication and queue reservation in the same write transaction.
func (s *Store) immediate(ctx context.Context, run func(*sql.Conn) error) error {
	conn, err := s.DB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
			defer cancel()
			conn.ExecContext(cleanup, "ROLLBACK")
		}
	}()
	if err = run(conn); err != nil {
		return err
	}
	if _, err = conn.ExecContext(ctx, "COMMIT"); err != nil {
		return err
	}
	committed = true
	return nil
}

func validRequest(r domain.VideoRequest) bool {
	if r.ChatID >= 0 || r.MessageID <= 0 || r.UserID <= 0 || len(r.VideoIDs) == 0 {
		return false
	}
	for _, id := range r.VideoIDs {
		if !domain.ValidSourceID(id) {
			return false
		}
	}
	return true
}

func (s *Store) queueNow() int64 {
	if s.now != nil {
		return s.now().Unix()
	}
	return time.Now().Unix()
}

// A reserved request belongs to a publication, including failed/retried copies.
// Expire only unused requests while holding the same lock as reservation.
func expireRequests(ctx context.Context, conn *sql.Conn, chatID, cutoff int64) error {
	_, err := conn.ExecContext(ctx, `DELETE FROM source_requests WHERE chat_id=? AND post_id IS NULL AND sent_at<=?`, chatID, cutoff)
	return err
}

func remember(ctx context.Context, conn *sql.Conn, r domain.VideoRequest) error {
	if !validRequest(r) {
		return domain.ErrInvalid
	}
	for _, id := range r.VideoIDs {
		_, err := conn.ExecContext(ctx, `INSERT INTO source_requests(chat_id,source_message_id,source_video_id,user_id,sent_at)
 SELECT ?,?,?,?,? WHERE NOT EXISTS (SELECT 1 FROM source_attributions WHERE chat_id=? AND source_message_id=? AND source_video_id=?)
 ON CONFLICT(chat_id,source_message_id,source_video_id) DO NOTHING`, r.ChatID, r.MessageID, id, r.UserID, r.SentAt, r.ChatID, r.MessageID, id)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) RememberRequest(ctx context.Context, r domain.VideoRequest) error {
	if !validRequest(r) {
		return domain.ErrInvalid
	}
	return s.immediate(ctx, func(conn *sql.Conn) error {
		cutoff := s.queueNow() - domain.DownloadRequestTTLSeconds
		if err := expireRequests(ctx, conn, r.ChatID, cutoff); err != nil {
			return err
		}
		// A delayed/replayed webhook must not restart the original deadline.
		if r.SentAt <= cutoff {
			return nil
		}
		return remember(ctx, conn, r)
	})
}

func (s *Store) RegisterDownloader(ctx context.Context, botID int64) error {
	if botID <= 0 {
		return domain.ErrInvalid
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO bot_senders(user_id) VALUES(?) ON CONFLICT(user_id) DO NOTHING`, botID)
	return err
}

type attribution struct {
	messageID int
	videoID   string
	userID    int64
	observed  string
	evidence  string
	verified  bool
}

func resolve(ctx context.Context, conn *sql.Conn, in domain.Incoming) (*attribution, error) {
	if !in.AllowFIFO || in.SenderBotID <= 0 || len(in.SourceVideoIDs) > 1 {
		return nil, nil
	}
	if len(in.SourceVideoIDs) == 1 && (!domain.ValidSourceID(in.SourceVideoIDs[0]) || (in.SourceEvidence != "youtube_url" && in.SourceEvidence != "youtube_filename" && in.SourceEvidence != "source_url" && in.SourceEvidence != "source_filename")) {
		return nil, domain.ErrInvalid
	}
	query := `SELECT source_message_id,source_video_id,user_id FROM source_requests WHERE chat_id=? AND post_id IS NULL AND source_message_id<?`
	args := []any{in.ChatID, in.MessageID}
	evidence := "fifo"
	if in.SourceRequest != nil {
		if in.SourceRequest.ChatID != in.ChatID || !validRequest(*in.SourceRequest) {
			return nil, domain.ErrInvalid
		}
		if err := remember(ctx, conn, *in.SourceRequest); err != nil {
			return nil, err
		}
		query += ` AND source_message_id=? AND user_id=?`
		args = append(args, in.SourceRequest.MessageID, in.SourceRequest.UserID)
		evidence = "reply"
	}
	observed := ""
	if len(in.SourceVideoIDs) == 1 {
		observed = in.SourceVideoIDs[0]
		if in.SourceEvidence == "youtube_filename" {
			// A bare 11-character filename can be either a YouTube ID or an
			// Instagram shortcode. Resolve it only against a unique queued source.
			query += ` AND source_video_id IN (?,?)`
			args = append(args, observed, "instagram:"+observed)
		} else {
			query += ` AND source_video_id=?`
			args = append(args, observed)
		}
		if evidence != "reply" {
			evidence = in.SourceEvidence
		}
	}
	matches := query
	query += ` ORDER BY source_message_id,source_video_id LIMIT 2`
	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	var candidates []attribution
	for rows.Next() {
		a := attribution{observed: observed, evidence: evidence, verified: evidence != "fifo"}
		if err = rows.Scan(&a.messageID, &a.videoID, &a.userID); err != nil {
			rows.Close()
			return nil, err
		}
		if in.SourceEvidence == "youtube_filename" && !domain.ValidYouTubeID(a.videoID) {
			a.observed = a.videoID
			if a.evidence != "reply" {
				a.evidence = "source_filename"
			}
		}
		candidates = append(candidates, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	// Metadata-only matches need one author; replies with several distinct URLs
	// need a video ID. FIFO alone deliberately follows the chosen ordering.
	if len(candidates) > 1 && evidence != "fifo" && (candidates[0].userID != candidates[1].userID || candidates[0].videoID != candidates[1].videoID) {
		return nil, nil
	}
	if evidence != "fifo" {
		var ambiguous int
		err = conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM (`+matches+`) WHERE user_id!=? OR source_video_id!=?`, append(args, candidates[0].userID, candidates[0].videoID)...).Scan(&ambiguous)
		if err != nil {
			return nil, err
		}
		if ambiguous > 0 {
			return nil, nil
		}
	}
	return &candidates[0], nil
}

func (s *Store) Register(ctx context.Context, in domain.Incoming) (p domain.Post, err error) {
	if in.ChatID >= 0 || in.MessageID <= 0 || strings.TrimSpace(in.UniqueID) == "" || (in.MediaType != "video" && in.MediaType != "video_note") {
		return p, domain.ErrInvalid
	}
	err = s.immediate(ctx, func(conn *sql.Conn) error {
		if err := expireRequests(ctx, conn, in.ChatID, s.queueNow()-domain.DownloadRequestTTLSeconds); err != nil {
			return err
		}
		var lookup error
		p, lookup = scanPost(conn.QueryRowContext(ctx, "SELECT "+postColumns+" FROM posts WHERE chat_id=? AND source_message_id=?", in.ChatID, in.MessageID))
		if lookup == nil {
			return nil
		}
		if !errors.Is(lookup, domain.ErrNotFound) {
			return lookup
		}
		owner := in.UploaderID
		if in.SenderBotID > 0 {
			owner = nil
			if _, err := conn.ExecContext(ctx, `INSERT INTO bot_senders(user_id) VALUES(?) ON CONFLICT(user_id) DO NOTHING`, in.SenderBotID); err != nil {
				return err
			}
		}
		a, err := resolve(ctx, conn, in)
		if err != nil {
			return err
		}
		if a != nil {
			owner = &a.userID
		}
		var mediaID int64
		err = conn.QueryRowContext(ctx, `INSERT INTO media(file_unique_id) VALUES(?) ON CONFLICT(file_unique_id) DO UPDATE SET file_unique_id=excluded.file_unique_id RETURNING id`, in.UniqueID).Scan(&mediaID)
		if err != nil {
			return err
		}
		p, err = scanPost(conn.QueryRowContext(ctx, `INSERT INTO posts(media_id,chat_id,source_message_id,uploader_user_id,media_type) VALUES(?,?,?,?,?) RETURNING `+postColumns, mediaID, in.ChatID, in.MessageID, owner, in.MediaType))
		if err != nil || a == nil {
			return err
		}
		_, err = conn.ExecContext(ctx, `UPDATE source_requests SET post_id=? WHERE chat_id=? AND source_message_id=? AND source_video_id=? AND post_id IS NULL`, p.ID, in.ChatID, a.messageID, a.videoID)
		if err != nil {
			return err
		}
		_, err = conn.ExecContext(ctx, `INSERT INTO source_attributions(post_id,chat_id,source_message_id,source_video_id,user_id,observed_source_video_id,verified,evidence) VALUES(?,?,?,?,?,NULLIF(?,''),?,?)`, p.ID, in.ChatID, a.messageID, a.videoID, a.userID, a.observed, a.verified, a.evidence)
		return err
	})
	return p, err
}
