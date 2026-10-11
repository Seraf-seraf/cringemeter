CREATE TABLE IF NOT EXISTS source_requests (
 chat_id INTEGER NOT NULL CHECK (chat_id<0),
 source_message_id INTEGER NOT NULL CHECK (source_message_id>0),
 source_video_id TEXT NOT NULL CHECK (length(source_video_id) BETWEEN 1 AND 128),
 user_id INTEGER NOT NULL CHECK (user_id>0),
 sent_at INTEGER NOT NULL,
 post_id INTEGER UNIQUE REFERENCES posts(id),
 PRIMARY KEY (chat_id,source_message_id,source_video_id)
);
-- statement
CREATE TABLE IF NOT EXISTS source_attributions (
 post_id INTEGER PRIMARY KEY REFERENCES posts(id),
 chat_id INTEGER NOT NULL,
 source_message_id INTEGER NOT NULL,
 source_video_id TEXT NOT NULL CHECK (length(source_video_id) BETWEEN 1 AND 128),
 user_id INTEGER NOT NULL CHECK (user_id>0),
 observed_source_video_id TEXT,
 verified INTEGER NOT NULL CHECK (verified IN (0,1)),
 evidence TEXT NOT NULL CHECK (evidence IN ('reply','youtube_url','youtube_filename','source_url','source_filename','fifo')),
 CHECK ((evidence='fifo' AND verified=0 AND observed_source_video_id IS NULL) OR (evidence!='fifo' AND verified=1))
);
-- statement
INSERT INTO source_attributions(post_id,chat_id,source_message_id,source_video_id,user_id,observed_source_video_id,verified,evidence)
SELECT post_id,chat_id,source_message_id,youtube_video_id,user_id,observed_youtube_video_id,verified,evidence FROM download_attributions WHERE true
ON CONFLICT(post_id) DO NOTHING;
-- statement
INSERT INTO source_requests(chat_id,source_message_id,source_video_id,user_id,sent_at,post_id)
SELECT chat_id,source_message_id,youtube_video_id,user_id,sent_at,post_id FROM download_requests r
WHERE r.post_id IS NOT NULL OR NOT EXISTS (
 SELECT 1 FROM source_attributions a WHERE a.chat_id=r.chat_id AND a.source_message_id=r.source_message_id AND a.source_video_id=r.youtube_video_id
)
ON CONFLICT(chat_id,source_message_id,source_video_id) DO NOTHING;
