CREATE TABLE cooldowns (
    key TEXT PRIMARY KEY,
    version INTEGER NOT NULL CHECK (version = 1),
    commit_until TEXT NOT NULL CHECK (length(commit_until) = 30 AND substr(commit_until, 11, 1) = 'T' AND substr(commit_until, 20, 1) = '.' AND substr(commit_until, 30, 1) = 'Z'),
    created_at TEXT NOT NULL CHECK (length(created_at) = 30 AND substr(created_at, 11, 1) = 'T' AND substr(created_at, 20, 1) = '.' AND substr(created_at, 30, 1) = 'Z'),
    updated_at TEXT NOT NULL CHECK (length(updated_at) = 30 AND substr(updated_at, 11, 1) = 'T' AND substr(updated_at, 20, 1) = '.' AND substr(updated_at, 30, 1) = 'Z')
);

CREATE TABLE decision_records (
    id TEXT PRIMARY KEY,
    version INTEGER NOT NULL CHECK (version = 1),
    idempotency_key TEXT NOT NULL UNIQUE,
    proposal BLOB NOT NULL,
    proposal_hash TEXT NOT NULL,
    settlement BLOB NOT NULL,
    settlement_hash TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('open', 'closed')),
    created_at TEXT NOT NULL CHECK (length(created_at) = 30 AND substr(created_at, 11, 1) = 'T' AND substr(created_at, 20, 1) = '.' AND substr(created_at, 30, 1) = 'Z'),
    closed_at TEXT CHECK (closed_at IS NULL OR (length(closed_at) = 30 AND substr(closed_at, 11, 1) = 'T' AND substr(closed_at, 20, 1) = '.' AND substr(closed_at, 30, 1) = 'Z')),
    updated_at TEXT NOT NULL CHECK (length(updated_at) = 30 AND substr(updated_at, 11, 1) = 'T' AND substr(updated_at, 20, 1) = '.' AND substr(updated_at, 30, 1) = 'Z'),
    CHECK ((status = 'open' AND closed_at IS NULL) OR (status = 'closed' AND closed_at IS NOT NULL))
);

CREATE TABLE outbox_effects (
    outbox_id TEXT PRIMARY KEY REFERENCES outbox_messages(id),
    version INTEGER NOT NULL CHECK (version = 1),
    cooldown_key TEXT,
    cooldown_commit_until TEXT CHECK (cooldown_commit_until IS NULL OR (length(cooldown_commit_until) = 30 AND substr(cooldown_commit_until, 11, 1) = 'T' AND substr(cooldown_commit_until, 20, 1) = '.' AND substr(cooldown_commit_until, 30, 1) = 'Z')),
    decision_id TEXT UNIQUE REFERENCES decision_records(id),
    created_at TEXT NOT NULL CHECK (length(created_at) = 30 AND substr(created_at, 11, 1) = 'T' AND substr(created_at, 20, 1) = '.' AND substr(created_at, 30, 1) = 'Z'),
    CHECK (
        (cooldown_key IS NOT NULL AND cooldown_commit_until IS NOT NULL AND decision_id IS NULL)
        OR
        (cooldown_key IS NULL AND cooldown_commit_until IS NULL AND decision_id IS NOT NULL)
    )
);

CREATE INDEX idx_cooldowns_commit_until ON cooldowns(commit_until);
CREATE INDEX idx_decision_records_status ON decision_records(status, created_at);
CREATE INDEX idx_outbox_effects_cooldown ON outbox_effects(cooldown_key);
CREATE INDEX idx_outbox_effects_decision ON outbox_effects(decision_id);
