package turso

import (
	"context"
	"database/sql"
)

type BotVoteCleanup struct {
	Removed     int64 `json:"removed"`
	Remaining   int64 `json:"remaining_bot_votes"`
	HumanBefore int64 `json:"human_votes_before"`
	HumanAfter  int64 `json:"human_votes_after"`
}

// Explicit cleanup of vote pools owned by registered bot senders, not ballots
// cast by a bot on a human's post. Author reassignment uses the same write lock.
func (s *Store) RemoveBotVotes(ctx context.Context) (report BotVoteCleanup, err error) {
	if err = s.Check(ctx); err != nil {
		return report, err
	}
	err = s.immediate(ctx, func(conn *sql.Conn) error {
		const humanCount = `SELECT COUNT(*) FROM author_votes WHERE uploader_user_id NOT IN (SELECT user_id FROM bot_senders)`
		if e := conn.QueryRowContext(ctx, humanCount).Scan(&report.HumanBefore); e != nil {
			return e
		}
		// Legacy ballots may be the only record of a known human participant.
		// Keep those identities before deleting bot-owned pools.
		_, e := conn.ExecContext(ctx, `INSERT INTO participants(chat_id,user_id,username,name)
 SELECT DISTINCT p.chat_id,v.user_id,'','' FROM author_votes v JOIN posts p ON p.media_id=v.media_id AND p.uploader_user_id=v.uploader_user_id
 WHERE p.chat_id<0 AND v.user_id>0 AND v.uploader_user_id IN (SELECT user_id FROM bot_senders) AND v.user_id NOT IN (SELECT user_id FROM bot_senders)
 ON CONFLICT(chat_id,user_id) DO NOTHING`)
		if e != nil {
			return e
		}
		result, e := conn.ExecContext(ctx, `DELETE FROM author_votes WHERE uploader_user_id IN (SELECT user_id FROM bot_senders)`)
		if e != nil {
			return e
		}
		if report.Removed, e = result.RowsAffected(); e != nil {
			return e
		}
		// Bots do not participate in tie ordering; clear their stale stored scores.
		if _, e = conn.ExecContext(ctx, `UPDATE score_positions SET score=0 WHERE user_id IN (SELECT user_id FROM bot_senders)`); e != nil {
			return e
		}
		if e = conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM author_votes WHERE uploader_user_id IN (SELECT user_id FROM bot_senders)`).Scan(&report.Remaining); e != nil {
			return e
		}
		return conn.QueryRowContext(ctx, humanCount).Scan(&report.HumanAfter)
	})
	return report, err
}
