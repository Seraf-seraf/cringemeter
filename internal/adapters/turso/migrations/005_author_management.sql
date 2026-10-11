CREATE TABLE IF NOT EXISTS participants (
 chat_id INTEGER NOT NULL CHECK (chat_id<0),
 user_id INTEGER NOT NULL CHECK (user_id>0),
 username TEXT NOT NULL,
 name TEXT NOT NULL,
 PRIMARY KEY (chat_id,user_id)
);
-- statement
CREATE TABLE IF NOT EXISTS author_changes (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 chat_id INTEGER NOT NULL,
 post_id INTEGER NOT NULL REFERENCES posts(id),
 actor_user_id INTEGER NOT NULL CHECK (actor_user_id>0),
 old_user_id INTEGER NOT NULL CHECK (old_user_id>=0),
 new_user_id INTEGER NOT NULL CHECK (new_user_id>0),
 changed_at INTEGER NOT NULL
);
