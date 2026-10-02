-- Builds data/memory/brain.db and data/cron/jobs.db for the v3 fixture.
-- brain.db
CREATE TABLE agents (id TEXT PRIMARY KEY, alias TEXT UNIQUE, created_at TEXT);
INSERT INTO agents VALUES ('a-default','default','2026-09-01T00:00:00Z'), ('a-coder','coder','2026-09-01T00:00:00Z');
CREATE TABLE memories (id TEXT PRIMARY KEY, key TEXT, content TEXT, category TEXT DEFAULT 'core', embedding BLOB,
  created_at TEXT, updated_at TEXT, session_id TEXT, namespace TEXT DEFAULT 'default', importance REAL,
  superseded_by TEXT, kind TEXT, pinned INT, tenant_id TEXT, principal_id TEXT, agent_id TEXT NOT NULL);
INSERT INTO memories (id,key,content,category,created_at,superseded_by,agent_id) VALUES
 ('m1','pet','Rina has a cat called Miso','core','2026-09-02T00:00:00Z',NULL,'a-default'),
 ('m2','editor','Rina uses Neovim','core','2026-09-03T00:00:00Z',NULL,'a-default'),
 ('m3','old','Rina uses VS Code','core','2026-09-01T00:00:00Z','m2','a-default'),
 ('m4','standup','Daily standup at 9','daily','2026-09-04T00:00:00Z',NULL,'a-default'),
 ('m5','lang','Prefers Go over Rust','preference','2026-09-05T00:00:00Z',NULL,'a-default'),
 ('m6','coder-only','Coder agent private note','core','2026-09-05T00:00:00Z',NULL,'a-coder');
