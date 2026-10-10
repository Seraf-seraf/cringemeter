CREATE TABLE IF NOT EXISTS score_positions (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 chat_id INTEGER NOT NULL,
 user_id INTEGER NOT NULL CHECK (user_id>0),
 score INTEGER NOT NULL,
 UNIQUE (chat_id,user_id)
);
-- statement
CREATE VIEW IF NOT EXISTS user_scores AS
SELECT p.chat_id,p.uploader_user_id AS user_id,SUM(COALESCE(v.balance,0)) AS score
FROM (SELECT DISTINCT chat_id,uploader_user_id,media_id FROM posts WHERE uploader_user_id>0) p
LEFT JOIN (SELECT media_id,uploader_user_id,SUM(CASE vote WHEN 1 THEN 1 ELSE -1 END) AS balance FROM author_votes WHERE user_id!=uploader_user_id GROUP BY media_id,uploader_user_id) v
 ON v.media_id=p.media_id AND v.uploader_user_id=p.uploader_user_id
GROUP BY p.chat_id,p.uploader_user_id;
-- statement
CREATE TRIGGER IF NOT EXISTS deny_self_vote_insert BEFORE INSERT ON author_votes WHEN NEW.user_id=NEW.uploader_user_id
BEGIN
 SELECT RAISE(ABORT,'self_vote_forbidden');
END;
-- statement
CREATE TRIGGER IF NOT EXISTS deny_self_vote_update BEFORE UPDATE ON author_votes WHEN NEW.user_id=NEW.uploader_user_id
BEGIN
 SELECT RAISE(ABORT,'self_vote_forbidden');
END;
