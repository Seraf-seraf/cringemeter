package turso

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/WillowWorks-io/libsql-client-go/libsql"
	"handler/internal/domain"
)

//go:embed migrations/001_initial.sql
var initialSchema string

type Store struct{ DB *sql.DB }

func Open(databaseURL, token string) (*Store, error) {
	connector, err := libsql.NewConnector(databaseURL, libsql.WithAuthToken(token))
	if err != nil {
		return nil, errors.New("database configuration invalid")
	}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(0)
	return &Store{DB: db}, nil
}

func (s *Store) Close() error { return s.DB.Close() }

func (s *Store) Migrate(ctx context.Context) error {
	var version int
	if err := s.DB.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		migrationFailure(ctx, "version_read", err)
		return err
	}
	if version > 1 {
		return errors.New("unsupported schema version")
	}
	if version == 1 {
		return s.Check(ctx)
	}
	if _, err := s.DB.ExecContext(ctx, "PRAGMA foreign_keys=ON"); err != nil {
		migrationFailure(ctx, "foreign_keys_enable", err)
		return err
	}
	// A non-transactional remote query closes its stream. BEGIN must be the
	// first operation on the reserved connection so subsequent DDL shares it.
	conn, err := s.DB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		migrationFailure(ctx, "begin", err)
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
	statements := append(strings.Split(initialSchema, ";"), "PRAGMA user_version=1", "COMMIT")
	for i, statement := range statements {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		if _, err = conn.ExecContext(ctx, statement); err != nil {
			migrationFailure(ctx, "schema_statement", err)
			slog.ErrorContext(ctx, "migration statement failed", "phase", "schema_statement", "statement_index", i)
			return err
		}
	}
	committed = true
	// Release the reserved connection before Check, also with a one-connection pool.
	conn.Close()
	return s.Check(ctx)
}

// Only allowlisted categories leave the adapter; driver errors may contain URLs.
func migrationFailure(ctx context.Context, phase string, err error) {
	code := "database_failed"
	for _, candidate := range []string{"SQLITE_AUTH", "SQLITE_READONLY", "SQLITE_BUSY", "SQLITE_ERROR", "syntax error", "failed to parse", "not authorized", "unsupported", "not supported", "not allowed", "transaction", "pragma", "closed", "permission"} {
		if strings.Contains(strings.ToLower(err.Error()), strings.ToLower(candidate)) {
			code = candidate
			break
		}
	}
	slog.ErrorContext(ctx, "migration failed", "phase", phase, "code", code)
}

func (s *Store) Check(ctx context.Context) error {
	var version int
	if err := s.DB.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version != 1 {
		return errors.New("schema migration required")
	}
	rows, err := s.DB.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		return errors.New("foreign key violation")
	}
	return rows.Err()
}

const postColumns = "id,media_id,chat_id,source_message_id,COALESCE(bot_message_id,0),state"

type scanner interface{ Scan(...any) error }

func scanPost(row scanner) (domain.Post, error) {
	var p domain.Post
	err := row.Scan(&p.ID, &p.MediaID, &p.ChatID, &p.SourceMessageID, &p.BotMessageID, &p.State)
	if errors.Is(err, sql.ErrNoRows) {
		return p, domain.ErrNotFound
	}
	return p, err
}

func (s *Store) Register(ctx context.Context, in domain.Incoming) (domain.Post, error) {
	if in.ChatID >= 0 || in.MessageID <= 0 || strings.TrimSpace(in.UniqueID) == "" || (in.MediaType != "video" && in.MediaType != "video_note") {
		return domain.Post{}, domain.ErrInvalid
	}
	var mediaID int64
	err := s.DB.QueryRowContext(ctx, `INSERT INTO media(file_unique_id) VALUES(?) ON CONFLICT(file_unique_id) DO UPDATE SET file_unique_id=excluded.file_unique_id RETURNING id`, in.UniqueID).Scan(&mediaID)
	if err != nil {
		return domain.Post{}, err
	}
	return scanPost(s.DB.QueryRowContext(ctx, `INSERT INTO posts(media_id,chat_id,source_message_id,uploader_user_id,media_type) VALUES(?,?,?,?,?) ON CONFLICT(chat_id,source_message_id) DO UPDATE SET source_message_id=excluded.source_message_id RETURNING `+postColumns, mediaID, in.ChatID, in.MessageID, in.UploaderID, in.MediaType))
}

func (s *Store) Claim(ctx context.Context, id, now, until int64) (bool, error) {
	// An expired claim may have reached Telegram: never take it over for a second copy.
	if _, err := s.DB.ExecContext(ctx, `UPDATE posts SET state='copy_unknown',claim_until=NULL WHERE id=? AND state='copying' AND claim_until<=?`, id, now); err != nil {
		return false, err
	}
	r, err := s.DB.ExecContext(ctx, `UPDATE posts SET state='copying',claim_until=? WHERE id=? AND state='pending'`, until, id)
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	return n == 1, err
}

func (s *Store) Post(ctx context.Context, id int64) (domain.Post, error) {
	return scanPost(s.DB.QueryRowContext(ctx, "SELECT "+postColumns+" FROM posts WHERE id=?", id))
}

func (s *Store) CopyResult(ctx context.Context, id int64, messageID int, state string, now int64) error {
	if (state == domain.Copied && messageID <= 0) || (state != domain.Copied && state != domain.CopyFailed && state != domain.CopyUnknown) {
		return domain.ErrInvalid
	}
	r, err := s.DB.ExecContext(ctx, `UPDATE posts SET state=?,bot_message_id=NULLIF(?,0),copied_at=CASE WHEN ?='copied' THEN ? ELSE NULL END,claim_until=NULL WHERE id=? AND (state='copying' OR (state='copy_unknown' AND ?='copied'))`, state, messageID, state, now, id, state)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return domain.ErrConflict
	}
	return nil
}

func (s *Store) Deleted(ctx context.Context, id, now int64) error {
	r, err := s.DB.ExecContext(ctx, `UPDATE posts SET state='done',source_deleted_at=? WHERE id=? AND state='copied' AND bot_message_id IS NOT NULL`, now, id)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return domain.ErrConflict
	}
	return nil
}

func (s *Store) Vote(ctx context.Context, postID, chatID int64, messageID int, userID int64, vote int, now int64) (int64, error) {
	if userID <= 0 || (vote != 0 && vote != 1) {
		return 0, domain.ErrInvalid
	}
	var mediaID int64
	err := s.DB.QueryRowContext(ctx, `INSERT INTO votes(media_id,user_id,vote,updated_at) SELECT media_id,?,?,? FROM posts WHERE id=? AND chat_id=? AND bot_message_id=? AND state IN ('copied','done') ON CONFLICT(media_id,user_id) DO UPDATE SET vote=excluded.vote,updated_at=excluded.updated_at RETURNING media_id`, userID, vote, now, postID, chatID, messageID).Scan(&mediaID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, domain.ErrNotFound
	}
	return mediaID, err
}

func (s *Store) Counts(ctx context.Context, mediaID int64) (domain.Counts, error) {
	var c domain.Counts
	err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(vote),0),COUNT(*)-COALESCE(SUM(vote),0) FROM votes WHERE media_id=?`, mediaID).Scan(&c.Cringe, &c.NonCringe)
	return c, err
}

func (s *Store) Copies(ctx context.Context, mediaID, afterID int64, limit int) ([]domain.Post, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+postColumns+" FROM posts WHERE media_id=? AND id>? AND bot_message_id IS NOT NULL AND state IN ('copied','done') ORDER BY id LIMIT ?", mediaID, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var posts []domain.Post
	for rows.Next() {
		p, err := scanPost(rows)
		if err != nil {
			return nil, err
		}
		posts = append(posts, p)
	}
	return posts, rows.Err()
}

func (s *Store) Top(ctx context.Context, chatID int64, minVotes, limit int) ([]domain.Rank, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT v.media_id,COALESCE((SELECT bot_message_id FROM posts p WHERE p.media_id=v.media_id AND p.chat_id=? AND p.state IN ('copied','done') AND p.bot_message_id IS NOT NULL ORDER BY p.id DESC LIMIT 1),0),SUM(v.vote),COUNT(*)-SUM(v.vote) FROM votes v WHERE EXISTS(SELECT 1 FROM posts p WHERE p.media_id=v.media_id AND p.chat_id=?) GROUP BY v.media_id HAVING COUNT(*)>=? ORDER BY 1.0*SUM(v.vote)/COUNT(*) DESC,COUNT(*) DESC,v.media_id ASC LIMIT ?`, chatID, chatID, minVotes, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ranks []domain.Rank
	for rows.Next() {
		var r domain.Rank
		if err := rows.Scan(&r.MediaID, &r.MessageID, &r.Counts.Cringe, &r.Counts.NonCringe); err != nil {
			return nil, err
		}
		ranks = append(ranks, r)
	}
	return ranks, rows.Err()
}

func (s *Store) Unavailable(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE posts SET bot_message_id=NULL WHERE id=? AND state='done'`, id)
	return err
}
