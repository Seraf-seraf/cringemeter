CREATE TABLE IF NOT EXISTS media (
 id INTEGER PRIMARY KEY,
 file_unique_id TEXT NOT NULL UNIQUE CHECK (length(file_unique_id) > 0),
 first_seen_at INTEGER NOT NULL DEFAULT (unixepoch())
);
CREATE TABLE IF NOT EXISTS posts (
 id INTEGER PRIMARY KEY,
 media_id INTEGER NOT NULL REFERENCES media(id),
 chat_id INTEGER NOT NULL,
 source_message_id INTEGER NOT NULL,
 bot_message_id INTEGER,
 uploader_user_id INTEGER,
 media_type TEXT NOT NULL CHECK (media_type IN ('video', 'video_note')),
 state TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'copying', 'copied', 'done', 'copy_failed', 'copy_unknown')),
 claim_until INTEGER,
 created_at INTEGER NOT NULL DEFAULT (unixepoch()),
 copied_at INTEGER,
 source_deleted_at INTEGER,
 UNIQUE (chat_id, source_message_id),
 UNIQUE (chat_id, bot_message_id)
);
CREATE TABLE IF NOT EXISTS votes (
 media_id INTEGER NOT NULL REFERENCES media(id) ON DELETE CASCADE,
 user_id INTEGER NOT NULL,
 vote INTEGER NOT NULL CHECK (vote IN (0, 1)),
 updated_at INTEGER NOT NULL DEFAULT (unixepoch()),
 PRIMARY KEY (media_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_posts_media_id ON posts(media_id);
