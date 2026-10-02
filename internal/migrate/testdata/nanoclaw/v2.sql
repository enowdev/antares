-- Builds data/v2.db for the v2 fixture (M/src/db/schema.ts).
CREATE TABLE agent_groups (id TEXT PRIMARY KEY, name TEXT, folder TEXT UNIQUE, agent_provider TEXT, created_at TEXT);
INSERT INTO agent_groups VALUES ('g-main','Nano','main','claude','2026-05-01'), ('g-coder','Coder','coder','claude','2026-05-02');
CREATE TABLE messaging_groups (id TEXT PRIMARY KEY, channel_type TEXT, platform_id TEXT, instance TEXT, name TEXT, is_group INT,
  unknown_sender_policy TEXT, created_at TEXT, denied_at TEXT);
INSERT INTO messaging_groups VALUES ('m1','discord','123456789','default','general',1,'strict','2026-05-01',NULL),
 ('m2','telegram','-100555','default','tg',1,'strict','2026-05-01','2026-06-01');
CREATE TABLE container_configs (agent_group_id TEXT PRIMARY KEY, provider TEXT, model TEXT, effort TEXT, image_tag TEXT,
  assistant_name TEXT, max_messages_per_prompt INT, skills TEXT DEFAULT '"all"', mcp_servers TEXT DEFAULT '{}',
  packages_apt TEXT DEFAULT '[]', packages_npm TEXT DEFAULT '[]', additional_mounts TEXT DEFAULT '[]', updated_at TEXT, timezone TEXT, speed TEXT);
INSERT INTO container_configs (agent_group_id, provider, model, mcp_servers) VALUES
 ('g-main','claude','claude-opus-4-1','{"github":{"command":"npx","args":["-y","@modelcontextprotocol/server-github"],"env":{"GITHUB_TOKEN":"ghp_FAKEncGITHUB0007"}},"wiki":{"type":"http","url":"https://wiki.example/mcp"}}'),
 ('g-coder','claude','claude-sonnet-4-5','{}');
