-- Builds users/u_123/workspace/cron/jobs.db for the openhuman fixture.
CREATE TABLE cron_jobs (id TEXT PRIMARY KEY, expression TEXT, command TEXT, schedule TEXT, job_type TEXT,
  prompt TEXT, name TEXT, session_target TEXT, model TEXT, enabled INT, delivery TEXT, delete_after_run INT,
  created_at TEXT, next_run TEXT, last_run TEXT, last_status TEXT, last_output TEXT, agent_id TEXT);
INSERT INTO cron_jobs (id,schedule,job_type,prompt,name,enabled,created_at) VALUES
 ('c1','{"kind":"cron","expr":"30 7 * * *","tz":"Asia/Jakarta","active_hours":null}','agent','Plan my day.','Daily plan',1,'2026-09-01'),
 ('c2','{"kind":"every","every_ms":7200000}','flow',NULL,'Sync flow',1,'2026-09-02'),
 ('c3','{"kind":"at","at":"2026-12-01T00:00:00Z"}','agent','Happy December!','Once',1,'2026-09-03');
