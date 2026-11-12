-- 手机控制台：已配对设备、一次性配对码、推送订阅、监听开关。
-- 任务仍在原表，手机不另建一份库。

CREATE TABLE devices (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    token_hash TEXT NOT NULL UNIQUE,
    created_at TEXT NOT NULL,
    last_seen_at TEXT,
    revoked_at TEXT
);

CREATE TABLE pair_codes (
    id INTEGER PRIMARY KEY,
    code_hash TEXT NOT NULL UNIQUE,
    expires_at TEXT NOT NULL,
    used_at TEXT
);

CREATE TABLE push_subscriptions (
    id INTEGER PRIMARY KEY,
    device_id INTEGER NOT NULL,
    endpoint TEXT NOT NULL UNIQUE,
    p256dh TEXT NOT NULL,
    auth TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE phone_settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    lan_enabled INTEGER NOT NULL DEFAULT 0,
    remote_enabled INTEGER NOT NULL DEFAULT 0,
    port INTEGER NOT NULL DEFAULT 0
);

INSERT INTO phone_settings(id, lan_enabled, remote_enabled, port) VALUES (1, 0, 0, 0);
