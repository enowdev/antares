package store

// Schema DDL is written once and runs unmodified on SQLite and Postgres.
// Timestamps are stored as unix milliseconds (BIGINT) so both dialects agree.
var migrations = []string{
	`CREATE TABLE IF NOT EXISTS sessions (
		id            TEXT PRIMARY KEY,
		title         TEXT NOT NULL DEFAULT '',
		platform      TEXT NOT NULL DEFAULT 'web',
		channel_id    TEXT NOT NULL DEFAULT '',
		user_id       TEXT NOT NULL DEFAULT '',
		model         TEXT NOT NULL DEFAULT '',
		provider      TEXT NOT NULL DEFAULT '',
		workspace     TEXT NOT NULL DEFAULT '',
		message_count INTEGER NOT NULL DEFAULT 0,
		tokens_in     BIGINT NOT NULL DEFAULT 0,
		tokens_out    BIGINT NOT NULL DEFAULT 0,
		cost          DOUBLE PRECISION NOT NULL DEFAULT 0,
		archived      BOOLEAN NOT NULL DEFAULT FALSE,
		pinned        BOOLEAN NOT NULL DEFAULT FALSE,
		created_at    BIGINT NOT NULL,
		updated_at    BIGINT NOT NULL,
		meta          TEXT NOT NULL DEFAULT '{}'
	)`,
	`CREATE INDEX IF NOT EXISTS idx_sessions_updated ON sessions(updated_at DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_sessions_platform ON sessions(platform, user_id)`,

	`CREATE TABLE IF NOT EXISTS messages (
		id           TEXT PRIMARY KEY,
		session_id   TEXT NOT NULL,
		seq          BIGINT NOT NULL,
		role         TEXT NOT NULL,
		content      TEXT NOT NULL DEFAULT '',
		reasoning    TEXT NOT NULL DEFAULT '',
		tool_calls   TEXT NOT NULL DEFAULT '',
		tool_call_id TEXT NOT NULL DEFAULT '',
		tool_name    TEXT NOT NULL DEFAULT '',
		attachments  TEXT NOT NULL DEFAULT '',
		model        TEXT NOT NULL DEFAULT '',
		tokens_in    INTEGER NOT NULL DEFAULT 0,
		tokens_out   INTEGER NOT NULL DEFAULT 0,
		hidden       BOOLEAN NOT NULL DEFAULT FALSE,
		compacted    BOOLEAN NOT NULL DEFAULT FALSE,
		created_at   BIGINT NOT NULL,
		meta         TEXT NOT NULL DEFAULT '{}'
	)`,
	`CREATE INDEX IF NOT EXISTS idx_messages_session ON messages(session_id, seq)`,
	`CREATE INDEX IF NOT EXISTS idx_messages_created ON messages(created_at DESC)`,

	`CREATE TABLE IF NOT EXISTS memories (
		id         TEXT PRIMARY KEY,
		scope      TEXT NOT NULL DEFAULT 'global',
		scope_key  TEXT NOT NULL DEFAULT '',
		mem_key    TEXT NOT NULL DEFAULT '',
		content    TEXT NOT NULL,
		tags       TEXT NOT NULL DEFAULT '[]',
		source     TEXT NOT NULL DEFAULT '',
		pinned     BOOLEAN NOT NULL DEFAULT FALSE,
		created_at BIGINT NOT NULL,
		updated_at BIGINT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_memories_scope ON memories(scope, scope_key)`,

	`CREATE TABLE IF NOT EXISTS rag_chunks (
		id          TEXT PRIMARY KEY,
		collection  TEXT NOT NULL,
		doc_id      TEXT NOT NULL DEFAULT '',
		path        TEXT NOT NULL DEFAULT '',
		chunk_index INTEGER NOT NULL DEFAULT 0,
		content     TEXT NOT NULL,
		embedding   BLOB,
		meta        TEXT NOT NULL DEFAULT '{}',
		created_at  BIGINT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_chunks_collection ON rag_chunks(collection)`,

	`CREATE TABLE IF NOT EXISTS vps_hosts (
		id           TEXT PRIMARY KEY,
		label        TEXT NOT NULL DEFAULT '',
		host         TEXT NOT NULL,
		port         INTEGER NOT NULL DEFAULT 22,
		username     TEXT NOT NULL DEFAULT 'root',
		auth_method  TEXT NOT NULL DEFAULT 'password',
		password     TEXT NOT NULL DEFAULT '',
		private_key  TEXT NOT NULL DEFAULT '',
		passphrase   TEXT NOT NULL DEFAULT '',
		host_key     TEXT NOT NULL DEFAULT '',
		created_at   BIGINT NOT NULL,
		updated_at   BIGINT NOT NULL
	)`,
	// host_key added after the table shipped; ignore "duplicate column" on
	// re-run. (SQLite has no IF NOT EXISTS for ADD COLUMN; the migration runner
	// tolerates the error via a follow-up guard below.)
	`ALTER TABLE vps_hosts ADD COLUMN host_key TEXT NOT NULL DEFAULT ''`,

	`CREATE TABLE IF NOT EXISTS cron_jobs (
		id         TEXT PRIMARY KEY,
		name       TEXT NOT NULL,
		schedule   TEXT NOT NULL,
		prompt     TEXT NOT NULL,
		enabled    BOOLEAN NOT NULL DEFAULT TRUE,
		target     TEXT NOT NULL DEFAULT '',
		timezone   TEXT NOT NULL DEFAULT '',
		last_run   BIGINT,
		next_run   BIGINT,
		last_state TEXT NOT NULL DEFAULT '',
		created_at BIGINT NOT NULL,
		updated_at BIGINT NOT NULL,
		meta       TEXT NOT NULL DEFAULT '{}'
	)`,
	`CREATE TABLE IF NOT EXISTS cron_runs (
		id          TEXT PRIMARY KEY,
		job_id      TEXT NOT NULL,
		status      TEXT NOT NULL DEFAULT 'running',
		output      TEXT NOT NULL DEFAULT '',
		error       TEXT NOT NULL DEFAULT '',
		session_id  TEXT NOT NULL DEFAULT '',
		started_at  BIGINT NOT NULL,
		finished_at BIGINT
	)`,
	`CREATE INDEX IF NOT EXISTS idx_cron_runs_job ON cron_runs(job_id, started_at DESC)`,

	`CREATE TABLE IF NOT EXISTS usage_records (
		id         TEXT PRIMARY KEY,
		session_id TEXT NOT NULL DEFAULT '',
		provider   TEXT NOT NULL DEFAULT '',
		model      TEXT NOT NULL DEFAULT '',
		tokens_in  INTEGER NOT NULL DEFAULT 0,
		tokens_out INTEGER NOT NULL DEFAULT 0,
		cache_read INTEGER NOT NULL DEFAULT 0,
		cost       DOUBLE PRECISION NOT NULL DEFAULT 0,
		source     TEXT NOT NULL DEFAULT '',
		created_at BIGINT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_usage_created ON usage_records(created_at DESC)`,

	`CREATE TABLE IF NOT EXISTS pairings (
		id           TEXT PRIMARY KEY,
		platform     TEXT NOT NULL,
		external_id  TEXT NOT NULL,
		display_name TEXT NOT NULL DEFAULT '',
		status       TEXT NOT NULL DEFAULT 'pending',
		code         TEXT NOT NULL DEFAULT '',
		created_at   BIGINT NOT NULL,
		updated_at   BIGINT NOT NULL
	)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_pairings_ident ON pairings(platform, external_id)`,

	`CREATE TABLE IF NOT EXISTS kv (
		kv_key     TEXT PRIMARY KEY,
		kv_value   TEXT NOT NULL DEFAULT '',
		updated_at BIGINT NOT NULL
	)`,

	// Social Media accounts: credentials encrypted with the social master key.
	`CREATE TABLE IF NOT EXISTS social_accounts (
		id                 TEXT PRIMARY KEY,
		platform           TEXT NOT NULL,
		display_name       TEXT NOT NULL DEFAULT '',
		username           TEXT NOT NULL DEFAULT '',
		encrypted_password TEXT NOT NULL DEFAULT '',
		encrypted_recovery TEXT NOT NULL DEFAULT '',
		profile_url        TEXT NOT NULL DEFAULT '',
		status             TEXT NOT NULL DEFAULT 'not_created',
		rag_namespace      TEXT NOT NULL DEFAULT '',
		skill_name         TEXT NOT NULL DEFAULT '',
		last_checked_at    BIGINT,
		created_at         BIGINT NOT NULL,
		updated_at         BIGINT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_social_accounts_platform ON social_accounts(platform)`,
}

// sqliteFTS adds the FTS5 index and triggers that keep it in sync.
var sqliteFTS = []string{
	`CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
		content, session_id UNINDEXED, message_id UNINDEXED, tokenize='unicode61 remove_diacritics 2')`,
	`CREATE TRIGGER IF NOT EXISTS messages_fts_ins AFTER INSERT ON messages BEGIN
		INSERT INTO messages_fts(content, session_id, message_id) VALUES (new.content, new.session_id, new.id);
	END`,
	`CREATE TRIGGER IF NOT EXISTS messages_fts_del AFTER DELETE ON messages BEGIN
		DELETE FROM messages_fts WHERE message_id = old.id;
	END`,
	`CREATE VIRTUAL TABLE IF NOT EXISTS memories_fts USING fts5(
		content, memory_id UNINDEXED, tokenize='unicode61 remove_diacritics 2')`,
	`CREATE TRIGGER IF NOT EXISTS memories_fts_ins AFTER INSERT ON memories BEGIN
		INSERT INTO memories_fts(content, memory_id) VALUES (new.content, new.id);
	END`,
	`CREATE TRIGGER IF NOT EXISTS memories_fts_upd AFTER UPDATE ON memories BEGIN
		DELETE FROM memories_fts WHERE memory_id = old.id;
		INSERT INTO memories_fts(content, memory_id) VALUES (new.content, new.id);
	END`,
	`CREATE TRIGGER IF NOT EXISTS memories_fts_del AFTER DELETE ON memories BEGIN
		DELETE FROM memories_fts WHERE memory_id = old.id;
	END`,

	// Repair session tallies that a stale UpdateSession clobbered to 0 (the
	// "0 messages / 0 tokens" bug). Recompute message_count / tokens_in /
	// tokens_out from the messages table for any session whose stored count
	// disagrees. Idempotent: on a healthy DB the WHERE matches nothing.
	`UPDATE sessions SET
		message_count = (SELECT COUNT(*) FROM messages m WHERE m.session_id = sessions.id),
		tokens_in     = (SELECT COALESCE(SUM(tokens_in), 0) FROM messages m WHERE m.session_id = sessions.id),
		tokens_out    = (SELECT COALESCE(SUM(tokens_out), 0) FROM messages m WHERE m.session_id = sessions.id)
	WHERE message_count <> (SELECT COUNT(*) FROM messages m WHERE m.session_id = sessions.id)`,
}

// postgresFTS adds GIN indexes for tsvector search.
var postgresFTS = []string{
	`CREATE INDEX IF NOT EXISTS idx_messages_fts ON messages USING GIN (to_tsvector('simple', content))`,
	`CREATE INDEX IF NOT EXISTS idx_memories_fts ON memories USING GIN (to_tsvector('simple', content))`,
}

// sqliteV2 adds the RAG chunk lexical index (FTS5 with content-mirrored rows
// and triggers, plus a one-shot backfill) and the collection-revision counter.
// The FTS5 table is a plain (non-external-content) table so both DELETE and
// UPDATE stay simple and consistent even if a future writer bypasses the
// triggers by attaching to the DB directly — the triggers cover the standard
// PutChunks / DeleteCollection paths and are enough for correctness.
var sqliteV2 = []string{
	`CREATE VIRTUAL TABLE IF NOT EXISTS rag_chunks_fts USING fts5(
		content, chunk_id UNINDEXED, collection UNINDEXED, tokenize='unicode61 remove_diacritics 2')`,
	`INSERT INTO rag_chunks_fts(content, chunk_id, collection)
	 SELECT content, id, collection FROM rag_chunks
	 WHERE NOT EXISTS (SELECT 1 FROM rag_chunks_fts f WHERE f.chunk_id = rag_chunks.id)`,
	`CREATE TRIGGER IF NOT EXISTS rag_chunks_fts_ins AFTER INSERT ON rag_chunks BEGIN
		INSERT INTO rag_chunks_fts(content, chunk_id, collection) VALUES (new.content, new.id, new.collection);
	END`,
	`CREATE TRIGGER IF NOT EXISTS rag_chunks_fts_upd AFTER UPDATE ON rag_chunks BEGIN
		DELETE FROM rag_chunks_fts WHERE chunk_id = old.id;
		INSERT INTO rag_chunks_fts(content, chunk_id, collection) VALUES (new.content, new.id, new.collection);
	END`,
	`CREATE TRIGGER IF NOT EXISTS rag_chunks_fts_del AFTER DELETE ON rag_chunks BEGIN
		DELETE FROM rag_chunks_fts WHERE chunk_id = old.id;
	END`,

	`CREATE TABLE IF NOT EXISTS rag_collection_revisions (
		collection TEXT PRIMARY KEY,
		revision   BIGINT NOT NULL DEFAULT 0,
		dims       INTEGER NOT NULL DEFAULT 0
	)`,
	// Backfill (collection, dims) from existing rows so a legacy collection
	// already stores the shape it was built with; the app-level guard reads
	// this row to reject any new write with a different dimensionality.
	// length(embedding)/4 assumes the encodeEmbedding little-endian float32
	// packing that shipped with v1 (4 bytes per component). Rows sharing a
	// collection MUST share dims — this SELECT pins the largest observed
	// blob length so a mixed-legacy collection surfaces on the next write.
	`INSERT INTO rag_collection_revisions(collection, revision, dims)
	 SELECT collection, 0, MAX(length(embedding)/4) FROM rag_chunks
	 WHERE embedding IS NOT NULL
	 GROUP BY collection
	 ON CONFLICT(collection) DO UPDATE SET dims =
	   CASE WHEN rag_collection_revisions.dims = 0
	        THEN excluded.dims
	        ELSE rag_collection_revisions.dims END`,
	`CREATE TRIGGER IF NOT EXISTS rag_chunks_rev_ins AFTER INSERT ON rag_chunks BEGIN
		INSERT INTO rag_collection_revisions(collection, revision) VALUES (new.collection, 1)
		ON CONFLICT(collection) DO UPDATE SET revision = revision + 1;
	END`,
	`CREATE TRIGGER IF NOT EXISTS rag_chunks_rev_upd AFTER UPDATE ON rag_chunks BEGIN
		INSERT INTO rag_collection_revisions(collection, revision) VALUES (new.collection, 1)
		ON CONFLICT(collection) DO UPDATE SET revision = revision + 1;
	END`,
	`CREATE TRIGGER IF NOT EXISTS rag_chunks_rev_del AFTER DELETE ON rag_chunks BEGIN
		INSERT INTO rag_collection_revisions(collection, revision) VALUES (old.collection, 1)
		ON CONFLICT(collection) DO UPDATE SET revision = revision + 1;
	END`,
}

// postgresV2 mirrors sqliteV2 using tsvector/GIN for the lexical index and
// row-level triggers for the revision counter. Extension-free — no pgvector,
// no pg_trgm — the tsvector is expression-indexed inline.
var postgresV2 = []string{
	`CREATE INDEX IF NOT EXISTS idx_rag_chunks_fts ON rag_chunks USING GIN (to_tsvector('simple', content))`,

	`CREATE TABLE IF NOT EXISTS rag_collection_revisions (
		collection TEXT PRIMARY KEY,
		revision   BIGINT NOT NULL DEFAULT 0,
		dims       INTEGER NOT NULL DEFAULT 0
	)`,
	`INSERT INTO rag_collection_revisions(collection, revision, dims)
	 SELECT collection, 0, MAX(octet_length(embedding)/4) FROM rag_chunks
	 WHERE embedding IS NOT NULL
	 GROUP BY collection
	 ON CONFLICT(collection) DO UPDATE SET dims =
	   CASE WHEN rag_collection_revisions.dims = 0
	        THEN EXCLUDED.dims
	        ELSE rag_collection_revisions.dims END`,
	`CREATE OR REPLACE FUNCTION rag_chunks_rev_bump() RETURNS TRIGGER AS $$
	BEGIN
		IF (TG_OP = 'DELETE') THEN
			INSERT INTO rag_collection_revisions(collection, revision) VALUES (OLD.collection, 1)
			ON CONFLICT(collection) DO UPDATE SET revision = rag_collection_revisions.revision + 1;
			RETURN OLD;
		ELSE
			INSERT INTO rag_collection_revisions(collection, revision) VALUES (NEW.collection, 1)
			ON CONFLICT(collection) DO UPDATE SET revision = rag_collection_revisions.revision + 1;
			RETURN NEW;
		END IF;
	END;
	$$ LANGUAGE plpgsql`,
	`DROP TRIGGER IF EXISTS rag_chunks_rev_ins ON rag_chunks`,
	`CREATE TRIGGER rag_chunks_rev_ins AFTER INSERT ON rag_chunks
		FOR EACH ROW EXECUTE FUNCTION rag_chunks_rev_bump()`,
	`DROP TRIGGER IF EXISTS rag_chunks_rev_upd ON rag_chunks`,
	`CREATE TRIGGER rag_chunks_rev_upd AFTER UPDATE ON rag_chunks
		FOR EACH ROW EXECUTE FUNCTION rag_chunks_rev_bump()`,
	`DROP TRIGGER IF EXISTS rag_chunks_rev_del ON rag_chunks`,
	`CREATE TRIGGER rag_chunks_rev_del AFTER DELETE ON rag_chunks
		FOR EACH ROW EXECUTE FUNCTION rag_chunks_rev_bump()`,
}

// devicesV3 creates the paired-device table. A device token is stored only as
// its SHA-256 hex; revoked rows stay so the list can show them. Timestamps are
// unix milliseconds like every other table; last_seen_at and revoked_at are
// NULL until set. The DDL is portable across SQLite and Postgres.
var devicesV3 = []string{
	`CREATE TABLE IF NOT EXISTS devices (
		id           TEXT PRIMARY KEY,
		name         TEXT NOT NULL,
		platform     TEXT NOT NULL,
		token_hash   TEXT NOT NULL UNIQUE,
		created_at   BIGINT NOT NULL,
		last_seen_at BIGINT,
		revoked_at   BIGINT
	)`,
	`CREATE INDEX IF NOT EXISTS idx_devices_created ON devices(created_at DESC)`,
}
