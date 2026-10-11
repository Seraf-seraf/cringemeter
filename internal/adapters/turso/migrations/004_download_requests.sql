CREATE TABLE IF NOT EXISTS download_requests (
 chat_id INTEGER NOT NULL CHECK (chat_id<0),
 source_message_id INTEGER NOT NULL CHECK (source_message_id>0),
 youtube_video_id TEXT NOT NULL CHECK (length(youtube_video_id)=11),
 user_id INTEGER NOT NULL CHECK (user_id>0),
 sent_at INTEGER NOT NULL,
 post_id INTEGER UNIQUE REFERENCES posts(id),
 PRIMARY KEY (chat_id,source_message_id,youtube_video_id)
);
-- statement
CREATE TABLE IF NOT EXISTS download_attributions (
 post_id INTEGER PRIMARY KEY REFERENCES posts(id),
 chat_id INTEGER NOT NULL,
 source_message_id INTEGER NOT NULL,
 youtube_video_id TEXT NOT NULL,
 user_id INTEGER NOT NULL CHECK (user_id>0),
 observed_youtube_video_id TEXT,
 verified INTEGER NOT NULL CHECK (verified IN (0,1)),
 evidence TEXT NOT NULL CHECK (evidence IN ('reply','youtube_url','youtube_filename','fifo')),
 CHECK ((evidence='fifo' AND verified=0 AND observed_youtube_video_id IS NULL) OR (evidence!='fifo' AND verified=1))
);
-- statement
CREATE TABLE IF NOT EXISTS bot_senders (
 user_id INTEGER PRIMARY KEY CHECK (user_id>0)
);
