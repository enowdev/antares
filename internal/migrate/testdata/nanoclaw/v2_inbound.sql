-- Builds data/v2-sessions/g-main/s1/inbound.db (M/src/mailbox/sqlite/schema.ts).
CREATE TABLE messages_in (id TEXT PRIMARY KEY, seq INTEGER UNIQUE, kind TEXT NOT NULL, timestamp TEXT, status TEXT DEFAULT 'pending',
  process_after TEXT, recurrence TEXT, series_id TEXT, tries INT, trigger TEXT, platform_id TEXT, channel_type TEXT, thread_id TEXT,
  content TEXT NOT NULL, source_session_id TEXT, on_wake TEXT);
INSERT INTO messages_in (id,seq,kind,status,process_after,recurrence,series_id,content) VALUES
 ('t1',1,'task','completed','2026-09-01T07:00:00Z','0 7 * * *','ser-a','{"prompt":"Morning digest"}'),
 ('t2',2,'task','pending','2026-09-02T07:00:00Z','0 7 * * *','ser-a','{"prompt":"Morning digest"}'),
 ('t3',3,'task','pending','2026-10-20T09:00:00Z',NULL,NULL,'{"prompt":"Renew passport"}'),
 ('t4',4,'chat','pending',NULL,NULL,NULL,'{"text":"hi"}');
