package turso

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"log/slog"
	"strings"
	"time"
	"unicode"

	"github.com/WillowWorks-io/libsql-client-go/libsql"
	"handler/internal/domain"
)

//go:embed migrations/001_initial.sql
var initialSchema string

// Each trigger is one SQL statement even though its body contains semicolons.
//
//go:embed migrations/002_user_ranking.sql
var rankingSchema string

//go:embed migrations/003_net_scores.sql
var netSchema string

func legacySchemaObjects() map[string]string {
	names := []string{"media", "posts", "votes", "idx_posts_media_id", "author_votes", "score_positions", "user_scores", "rank_vote_insert", "rank_vote_update", "rank_post_insert", "legacy_vote_insert", "legacy_vote_update"}
	definitions := append(strings.Split(strings.TrimSpace(initialSchema), ";")[:4], strings.Split(strings.TrimSpace(rankingSchema), "\n-- statement\n")...)
	objects := make(map[string]string, len(names))
	for i, name := range names {
		objects[name] = definitions[i]
	}
	return objects
}

func schemaObjects() map[string]string {
	objects := legacySchemaObjects()
	for i, name := range []string{"score_positions", "user_scores", "deny_self_vote_insert", "deny_self_vote_update"} {
		objects[name] = strings.Split(netSchema, "\n-- statement\n")[i]
	}
	return objects
}

type Store struct{ DB *sql.DB }

var errMigrationRequired = errors.New("schema migration required")

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
	if err := s.Check(ctx); err == nil {
		return nil
	}
	// Accept exactly the known previous schema; unrelated definitions stay rejected.
	bootstrap := false
	if err := s.checkSchema(ctx, legacySchemaObjects()); err != nil {
		if !errors.Is(err, errMigrationRequired) {
			return err
		}
		bootstrap = true
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
	ranking := strings.Split(rankingSchema, "\n-- statement\n")
	var statements []string
	if bootstrap {
		statements = append(statements, strings.Split(initialSchema, ";")...)
		statements = append(statements, ranking[:3]...)
		statements = append(statements, "DELETE FROM votes", `INSERT INTO score_positions(chat_id,user_id,score)
 SELECT s.chat_id,s.user_id,s.score FROM user_scores s WHERE true
 ORDER BY s.chat_id,s.user_id ON CONFLICT(chat_id,user_id) DO NOTHING`)
		statements = append(statements, ranking[3:]...)
	}
	// Rebuild only score metadata. Keep vote rows, publication IDs and the
	// established tie order; changing formulas cannot reconstruct past net scores.
	net := strings.Split(netSchema, "\n-- statement\n")
	statements = append(statements,
		"DROP TRIGGER rank_vote_insert", "DROP TRIGGER rank_vote_update", "DROP TRIGGER rank_post_insert",
		"DROP VIEW user_scores",
		"ALTER TABLE score_positions RENAME TO score_positions_before_balance",
		net[0], net[1],
		`INSERT INTO score_positions(id,chat_id,user_id,score)
 SELECT p.id,s.chat_id,s.user_id,s.score FROM score_positions_before_balance p
 JOIN user_scores s ON s.chat_id=p.chat_id AND s.user_id=p.user_id ORDER BY p.id`,
		`INSERT INTO score_positions(chat_id,user_id,score)
 SELECT s.chat_id,s.user_id,s.score FROM user_scores s WHERE true
 ON CONFLICT(chat_id,user_id) DO NOTHING`,
		"DROP TABLE score_positions_before_balance")
	statements = append(statements, ranking[3:6]...)
	statements = append(statements, net[2:]...)
	statements = append(statements, "COMMIT")

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

func (s *Store) Check(ctx context.Context) error { return s.checkSchema(ctx, schemaObjects()) }

func (s *Store) checkSchema(ctx context.Context, expected map[string]string) error {
	// Turso forbids writing user_version: validate the actual objects.
	objects, err := s.DB.QueryContext(ctx, `SELECT name,sql FROM sqlite_master WHERE name IN ('media','posts','votes','idx_posts_media_id','author_votes','score_positions','user_scores','rank_vote_insert','rank_vote_update','rank_post_insert','legacy_vote_insert','legacy_vote_update','deny_self_vote_insert','deny_self_vote_update')`)
	if err != nil {
		return err
	}
	found := 0
	for objects.Next() {
		var name, definition string
		if err := objects.Scan(&name, &definition); err != nil {
			objects.Close()
			return err
		}
		if _, owned := expected[name]; !owned {
			if normalizeSchema(schemaObjects()[name]) != normalizeSchema(definition) {
				objects.Close()
				return errors.New("unsupported schema definition")
			}
			continue
		}
		if normalizeSchema(expected[name]) != normalizeSchema(definition) {
			objects.Close()
			return errors.New("unsupported schema definition")
		}
		found++
	}
	err = objects.Err()
	objects.Close()
	if err != nil {
		return err
	}
	if found != len(expected) {
		return errMigrationRequired
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

func normalizeSchema(sql string) string {
	var normalized strings.Builder
	quoted := false
	for _, char := range sql {
		if char == '\'' {
			quoted = !quoted
		}
		if !quoted {
			if unicode.IsSpace(char) {
				continue
			}
			char = unicode.ToLower(char)
		}
		normalized.WriteRune(char)
	}
	sql = normalized.String()
	sql = strings.Replace(sql, "createtableifnotexists", "createtable", 1)
	sql = strings.Replace(sql, "createindexifnotexists", "createindex", 1)
	sql = strings.Replace(sql, "createviewifnotexists", "createview", 1)
	sql = strings.Replace(sql, "createtriggerifnotexists", "createtrigger", 1)
	return strings.TrimSuffix(sql, ";")
}

const postColumns = "id,media_id,chat_id,source_message_id,COALESCE(bot_message_id,0),state,COALESCE(uploader_user_id,0)"

type scanner interface{ Scan(...any) error }

func scanPost(row scanner) (domain.Post, error) {
	var p domain.Post
	err := row.Scan(&p.ID, &p.MediaID, &p.ChatID, &p.SourceMessageID, &p.BotMessageID, &p.State, &p.UploaderID)
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
	err := s.DB.QueryRowContext(ctx, `INSERT INTO author_votes(media_id,uploader_user_id,user_id,vote,updated_at) SELECT media_id,COALESCE(uploader_user_id,0),?,?,? FROM posts WHERE id=? AND chat_id=? AND bot_message_id=? AND state IN ('copied','done') AND COALESCE(uploader_user_id,0)!=? ON CONFLICT(media_id,uploader_user_id,user_id) DO UPDATE SET vote=excluded.vote,updated_at=excluded.updated_at RETURNING media_id`, userID, vote, now, postID, chatID, messageID, userID).Scan(&mediaID)
	if errors.Is(err, sql.ErrNoRows) {
		var owner int64
		lookup := s.DB.QueryRowContext(ctx, `SELECT COALESCE(uploader_user_id,0) FROM posts WHERE id=? AND chat_id=? AND bot_message_id=? AND state IN ('copied','done')`, postID, chatID, messageID).Scan(&owner)
		if errors.Is(lookup, sql.ErrNoRows) {
			return 0, domain.ErrNotFound
		}
		if lookup != nil {
			return 0, lookup
		}
		if owner == userID {
			return 0, domain.ErrSelfVote
		}
		return 0, domain.ErrNotFound
	}
	if err != nil && strings.Contains(err.Error(), "self_vote_forbidden") {
		return 0, domain.ErrSelfVote
	}

	return mediaID, err
}

func (s *Store) Counts(ctx context.Context, postID int64) (domain.Counts, error) {
	var c domain.Counts
	err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(vote),0),COUNT(*)-COALESCE(SUM(vote),0) FROM author_votes v JOIN posts p ON p.media_id=v.media_id AND COALESCE(p.uploader_user_id,0)=v.uploader_user_id WHERE p.id=? AND v.user_id!=v.uploader_user_id`, postID).Scan(&c.Cringe, &c.NonCringe)
	return c, err
}

func (s *Store) Copies(ctx context.Context, postID, afterID int64, limit int) ([]domain.Post, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+postColumns+" FROM posts WHERE media_id=(SELECT media_id FROM posts WHERE id=?) AND COALESCE(uploader_user_id,0)=(SELECT COALESCE(uploader_user_id,0) FROM posts WHERE id=?) AND id>? AND bot_message_id IS NOT NULL AND state IN ('copied','done') ORDER BY id LIMIT ?", postID, postID, afterID, limit)
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

func (s *Store) Top(ctx context.Context, chatID int64) ([]domain.Rank, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT s.user_id,s.score FROM user_scores s
 JOIN score_positions r ON r.chat_id=s.chat_id AND r.user_id=s.user_id AND r.score=s.score
 WHERE s.chat_id=? AND s.score>0 ORDER BY s.score DESC,r.id ASC LIMIT 3`, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ranks []domain.Rank
	for rows.Next() {
		var r domain.Rank
		if err := rows.Scan(&r.UserID, &r.Score); err != nil {
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

// Explicit administrative cleanup, separate from schema migrations. Self votes
// are already excluded from both counters and net scores, so no rank changes.
func (s *Store) RemoveSelfVotes(ctx context.Context) (int64, error) {
	if err := s.Check(ctx); err != nil {
		return 0, err
	}
	result, err := s.DB.ExecContext(ctx, "DELETE FROM author_votes WHERE user_id=uploader_user_id")
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
