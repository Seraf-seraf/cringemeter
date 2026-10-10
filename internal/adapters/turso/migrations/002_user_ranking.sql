CREATE TABLE IF NOT EXISTS author_votes (
 media_id INTEGER NOT NULL REFERENCES media(id) ON DELETE CASCADE,
 uploader_user_id INTEGER NOT NULL CHECK (uploader_user_id>=0),
 user_id INTEGER NOT NULL CHECK (user_id>0),
 vote INTEGER NOT NULL CHECK (vote IN (0,1)),
 updated_at INTEGER NOT NULL,
 PRIMARY KEY (media_id,uploader_user_id,user_id)
);
-- statement
CREATE TABLE IF NOT EXISTS score_positions (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 chat_id INTEGER NOT NULL,
 user_id INTEGER NOT NULL CHECK (user_id>0),
 score INTEGER NOT NULL CHECK (score>=0),
 UNIQUE (chat_id,user_id)
);
-- statement
CREATE VIEW IF NOT EXISTS user_scores AS
SELECT p.chat_id,p.uploader_user_id AS user_id,SUM(COALESCE(v.cringe,0)) AS score
FROM (SELECT DISTINCT chat_id,uploader_user_id,media_id FROM posts WHERE uploader_user_id>0) p
LEFT JOIN (SELECT media_id,uploader_user_id,SUM(vote) AS cringe FROM author_votes GROUP BY media_id,uploader_user_id) v
 ON v.media_id=p.media_id AND v.uploader_user_id=p.uploader_user_id
GROUP BY p.chat_id,p.uploader_user_id;
-- statement
CREATE TRIGGER IF NOT EXISTS rank_vote_insert AFTER INSERT ON author_votes WHEN NEW.uploader_user_id>0
BEGIN
 INSERT INTO score_positions(chat_id,user_id,score)
 SELECT s.chat_id,s.user_id,s.score FROM user_scores s WHERE s.user_id=NEW.uploader_user_id
 ORDER BY s.chat_id,s.user_id
 ON CONFLICT(chat_id,user_id) DO UPDATE SET id=excluded.id,score=excluded.score WHERE score_positions.score!=excluded.score;
END;
-- statement
CREATE TRIGGER IF NOT EXISTS rank_vote_update AFTER UPDATE OF vote ON author_votes WHEN NEW.vote!=OLD.vote AND NEW.uploader_user_id>0
BEGIN
 INSERT INTO score_positions(chat_id,user_id,score)
 SELECT s.chat_id,s.user_id,s.score FROM user_scores s WHERE s.user_id=NEW.uploader_user_id
 ORDER BY s.chat_id,s.user_id
 ON CONFLICT(chat_id,user_id) DO UPDATE SET id=excluded.id,score=excluded.score WHERE score_positions.score!=excluded.score;
END;
-- statement
CREATE TRIGGER IF NOT EXISTS rank_post_insert AFTER INSERT ON posts WHEN NEW.uploader_user_id>0
BEGIN
 INSERT INTO score_positions(chat_id,user_id,score)
 SELECT s.chat_id,s.user_id,s.score FROM user_scores s WHERE s.chat_id=NEW.chat_id AND s.user_id=NEW.uploader_user_id
 ORDER BY s.chat_id,s.user_id
 ON CONFLICT(chat_id,user_id) DO UPDATE SET id=excluded.id,score=excluded.score WHERE score_positions.score!=excluded.score;
END;
-- statement
CREATE TRIGGER IF NOT EXISTS legacy_vote_insert BEFORE INSERT ON votes
BEGIN
 SELECT RAISE(ABORT,'legacy votes are read only');
END;
-- statement
CREATE TRIGGER IF NOT EXISTS legacy_vote_update BEFORE UPDATE ON votes
BEGIN
 SELECT RAISE(ABORT,'legacy votes are read only');
END;
