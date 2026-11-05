-- Builds data/cron/jobs.db for the v3 fixture.
CREATE TABLE cron_jobs (id TEXT PRIMARY KEY, expression TEXT, command TEXT, schedule TEXT, job_type TEXT DEFAULT 'shell',
  prompt TEXT, name TEXT, session_target TEXT DEFAULT 'isolated', model TEXT, enabled INT, delivery TEXT,
  delete_after_run INT, allowed_tools TEXT, created_at TEXT, next_run TEXT, last_run TEXT, last_status TEXT,
  last_output TEXT, source TEXT DEFAULT 'imperative', uses_memory INT, agent_alias TEXT);
INSERT INTO cron_jobs (id,expression,schedule,job_type,prompt,name,enabled,created_at) VALUES
 ('j-water','','{"kind":"every","every_ms":1800000}','agent','Remind me to drink water.','Hydrate',1,'2026-09-01T00:00:00Z'),
 ('j-dentist','','{"kind":"at","at":"2026-10-10T09:00:00Z"}','agent','Dentist at 10.','Dentist',1,'2026-09-02T00:00:00Z'),
 ('j-weekly','0 17 * * 5','{"kind":"cron","expr":"0 17 * * 5","tz":null}','agent','Write my weekly review.','Weekly review',0,'2026-09-03T00:00:00Z'),
 ('morning','','{"kind":"cron","expr":"0 8 * * 1-5"}','agent','dup of toml','Morning brief',1,'2026-09-04T00:00:00Z');
