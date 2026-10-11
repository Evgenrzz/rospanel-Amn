-- NaiveProxy: a node-only lane (internal/naive). The master never runs caddy-naive,
-- so its settings row holds the GLOBAL defaults every node inherits — the probe secret
-- and the masquerade URL — plus the toggle and port the master's own editor shows. A
-- node's on/off rides on its row; its port and display name ride in connections_config
-- with the other per-node connection settings, exactly like AmneziaWG (see 0061).
--
-- Each user carries one NaiveProxy password (the username is always "u<id>"), minted
-- the first time the lane is enabled for them. It is needed in clear for the Caddyfile
-- basic_auth and the naive+secure:// link, so it is encrypted at rest like the others.
ALTER TABLE settings ADD COLUMN naive_enabled        INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings ADD COLUMN naive_port           INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings ADD COLUMN naive_probe_secret   TEXT    NOT NULL DEFAULT '';
ALTER TABLE settings ADD COLUMN naive_masquerade_url TEXT    NOT NULL DEFAULT '';
ALTER TABLE settings ADD COLUMN naive_name           TEXT    NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN naive_enabled INTEGER;
ALTER TABLE users ADD COLUMN naive_pass TEXT NOT NULL DEFAULT '';
