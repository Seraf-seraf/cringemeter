package turso

import (
	"context"
	"database/sql"
	"handler/internal/domain"
)

func (s *Store) RememberParticipant(ctx context.Context, chatID int64, u domain.Participant) error {
	if chatID >= 0 || u.ID <= 0 {
		return domain.ErrInvalid
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO participants(chat_id,user_id,username,name) VALUES(?,?,?,?) ON CONFLICT(chat_id,user_id) DO UPDATE SET username=excluded.username,name=excluded.name`, chatID, u.ID, u.Username, u.Name)
	return err
}

const knownParticipants = `SELECT user_id FROM participants WHERE chat_id=? UNION SELECT uploader_user_id FROM posts WHERE chat_id=? AND uploader_user_id>0 UNION SELECT v.user_id FROM author_votes v JOIN posts p ON p.media_id=v.media_id AND COALESCE(p.uploader_user_id,0)=v.uploader_user_id WHERE p.chat_id=?`

func (s *Store) KnownParticipant(ctx context.Context, chatID, userID int64) (bool, error) {
	var known bool
	err := s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM (`+knownParticipants+`) u WHERE u.user_id=? AND NOT EXISTS (SELECT 1 FROM bot_senders b WHERE b.user_id=u.user_id))`, chatID, chatID, chatID, userID).Scan(&known)
	return known, err
}

func (s *Store) Participants(ctx context.Context, chatID int64, page int) (out domain.ParticipantPage, err error) {
	if chatID >= 0 || page < 0 || page > 100000 {
		return out, domain.ErrInvalid
	}
	// Rows, page count and a clamped index come from one consistent SQL snapshot.
	rows, err := s.DB.QueryContext(ctx, `WITH users AS (
 SELECT u.user_id,COALESCE(p.username,'') username,COALESCE(p.name,'') name
 FROM (`+knownParticipants+`) u LEFT JOIN participants p ON p.user_id=u.user_id AND p.chat_id=?
 WHERE NOT EXISTS(SELECT 1 FROM bot_senders b WHERE b.user_id=u.user_id)),
 current AS (SELECT MAX(1,(COUNT(*)+?-1)/?) pages,MIN(?,MAX(0,(COUNT(*)-1)/?)) idx FROM users),
 listed AS (SELECT * FROM users ORDER BY user_id LIMIT ? OFFSET (SELECT idx*? FROM current))
 SELECT listed.user_id,listed.username,listed.name,current.idx,current.pages FROM current LEFT JOIN listed ON 1=1 ORDER BY listed.user_id`,
		chatID, chatID, chatID, chatID, domain.AuthorPageSize, domain.AuthorPageSize, page, domain.AuthorPageSize, domain.AuthorPageSize, domain.AuthorPageSize)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	out.Users = make([]domain.Participant, 0)
	for rows.Next() {
		var id sql.NullInt64
		var username, name sql.NullString
		if err = rows.Scan(&id, &username, &name, &out.Index, &out.Pages); err != nil {
			return out, err
		}
		if id.Valid {
			out.Users = append(out.Users, domain.Participant{ID: id.Int64, Username: username.String, Name: name.String})
		}
	}
	return out, rows.Err()
}

// Change a shared file/author vote pool as a unit, under the same write lock as
// vote statements. An optimistic revision rejects stale administrator menus.
func (s *Store) ChangeAuthor(ctx context.Context, a domain.AuthorAction, now int64) (out domain.Post, err error) {
	if a.UserID <= 0 || a.TargetID <= 0 {
		return out, domain.ErrInvalid
	}
	err = s.immediate(ctx, func(conn *sql.Conn) error {
		p, e := scanPost(conn.QueryRowContext(ctx, "SELECT "+postColumns+" FROM posts WHERE id=? AND chat_id=? AND bot_message_id=? AND state IN ('copied','done')", a.PostID, a.ChatID, a.MessageID))
		if e != nil {
			return e
		}
		if p.AuthorRevision != a.ExpectedRevision {
			return domain.ErrConflict
		}
		if p.UploaderID == a.TargetID {
			out = p
			return nil
		}
		var foreign bool
		e = conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM posts WHERE media_id=? AND chat_id!=? AND COALESCE(uploader_user_id,0) IN (?,?))`, p.MediaID, p.ChatID, p.UploaderID, a.TargetID).Scan(&foreign)
		if e != nil {
			return e
		}
		if foreign {
			return domain.ErrConflict
		}
		type position struct{ user, id, score int64 }
		rows, e := conn.QueryContext(ctx, `SELECT user_id,id,score FROM score_positions WHERE chat_id=? AND user_id IN (?,?)`, p.ChatID, p.UploaderID, a.TargetID)
		if e != nil {
			return e
		}
		var previous []position
		for rows.Next() {
			var r position
			if e = rows.Scan(&r.user, &r.id, &r.score); e != nil {
				rows.Close()
				return e
			}
			previous = append(previous, r)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		_, e = conn.ExecContext(ctx, `INSERT INTO author_changes(chat_id,post_id,actor_user_id,old_user_id,new_user_id,changed_at) SELECT chat_id,id,?,COALESCE(uploader_user_id,0),?,? FROM posts WHERE chat_id=? AND media_id=? AND COALESCE(uploader_user_id,0) IN (?,?)`, a.UserID, a.TargetID, now, p.ChatID, p.MediaID, p.UploaderID, a.TargetID)
		if e != nil {
			return e
		}
		_, e = conn.ExecContext(ctx, `UPDATE posts SET uploader_user_id=? WHERE chat_id=? AND media_id=? AND COALESCE(uploader_user_id,0)=?`, a.TargetID, p.ChatID, p.MediaID, p.UploaderID)
		if e != nil {
			return e
		}
		_, e = conn.ExecContext(ctx, `INSERT INTO author_votes(media_id,uploader_user_id,user_id,vote,updated_at) SELECT media_id,?,user_id,vote,updated_at FROM author_votes WHERE media_id=? AND uploader_user_id=? AND user_id!=? ON CONFLICT(media_id,uploader_user_id,user_id) DO UPDATE SET vote=excluded.vote,updated_at=excluded.updated_at WHERE excluded.updated_at>author_votes.updated_at`, a.TargetID, p.MediaID, p.UploaderID, a.TargetID)
		if e != nil {
			return e
		}
		_, e = conn.ExecContext(ctx, `DELETE FROM author_votes WHERE media_id=? AND uploader_user_id=?`, p.MediaID, p.UploaderID)
		if e != nil {
			return e
		}
		for _, userID := range []int64{p.UploaderID, a.TargetID} {
			if userID <= 0 {
				continue
			}
			_, e = conn.ExecContext(ctx, `INSERT INTO score_positions(chat_id,user_id,score) SELECT ?,?,COALESCE((SELECT score FROM user_scores WHERE chat_id=? AND user_id=?),0) ON CONFLICT(chat_id,user_id) DO UPDATE SET id=excluded.id,score=excluded.score WHERE score_positions.score!=excluded.score`, p.ChatID, userID, p.ChatID, userID)
			if e != nil {
				return e
			}
		}
		// Transfers can cause intermediate trigger scores inside the transaction.
		// Preserve the established tie order if the final score did not change.
		for _, r := range previous {
			_, e = conn.ExecContext(ctx, `UPDATE score_positions SET id=? WHERE chat_id=? AND user_id=? AND score=?`, r.id, p.ChatID, r.user, r.score)
			if e != nil {
				return e
			}
		}
		out, e = scanPost(conn.QueryRowContext(ctx, "SELECT "+postColumns+" FROM posts WHERE id=?", p.ID))
		return e
	})
	return out, err
}
