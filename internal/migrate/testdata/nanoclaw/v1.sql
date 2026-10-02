-- Builds store/messages.db for the v1 fixture (DDL from V1/src/db.ts).
CREATE TABLE registered_groups (jid TEXT PRIMARY KEY, name TEXT NOT NULL, folder TEXT NOT NULL UNIQUE,
  trigger_pattern TEXT NOT NULL, added_at TEXT NOT NULL, container_config TEXT, requires_trigger INTEGER DEFAULT 1, is_main INTEGER DEFAULT 0);
INSERT INTO registered_groups VALUES
 ('120363@g.us','Main chat','main','@Andy','2026-01-01T00:00:00Z',NULL,0,1),
 ('tg:-100777','Family','family','@Andy','2026-01-02T00:00:00Z',NULL,1,0);
CREATE TABLE scheduled_tasks (id TEXT PRIMARY KEY, group_folder TEXT NOT NULL, chat_jid TEXT NOT NULL,
  prompt TEXT NOT NULL, schedule_type TEXT NOT NULL, schedule_value TEXT NOT NULL, next_run TEXT,
  last_run TEXT, last_result TEXT, status TEXT DEFAULT 'active', created_at TEXT NOT NULL,
  context_mode TEXT DEFAULT 'isolated', script TEXT);
INSERT INTO scheduled_tasks (id,group_folder,chat_jid,prompt,schedule_type,schedule_value,status,created_at,script) VALUES
 ('task-1','main','120363@g.us','Send me the morning summary','cron','0 7 * * *','active','2026-01-03',NULL),
 ('task-2','main','120363@g.us','Check the server status','interval','900000','paused','2026-01-04','curl -s localhost'),
 ('task-3','family','tg:-100777','Remind everyone about dinner','once','2026-11-01T18:00:00','active','2026-01-05',NULL),
 ('task-4','main','120363@g.us','Old finished task','cron','0 1 * * *','completed','2026-01-06',NULL);
