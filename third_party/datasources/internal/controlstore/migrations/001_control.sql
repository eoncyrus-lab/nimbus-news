CREATE TABLE job_runs (
    id TEXT PRIMARY KEY,
    version INTEGER NOT NULL CHECK (version = 1),
    kind TEXT NOT NULL,
    producer TEXT NOT NULL,
    trace_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'cancelled')),
    error_code TEXT,
    error_message TEXT,
    error_retryable INTEGER CHECK (error_retryable IS NULL OR error_retryable IN (0, 1)),
    created_at TEXT NOT NULL,
    started_at TEXT,
    finished_at TEXT,
    updated_at TEXT NOT NULL,
    UNIQUE (kind, producer, idempotency_key)
);

CREATE TABLE step_runs (
    id TEXT PRIMARY KEY,
    version INTEGER NOT NULL CHECK (version = 1),
    job_id TEXT NOT NULL REFERENCES job_runs(id),
    name TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'cancelled')),
    attempt INTEGER NOT NULL DEFAULT 0 CHECK (attempt >= 0),
    available_at TEXT NOT NULL CHECK (length(available_at) = 30 AND substr(available_at, 11, 1) = 'T' AND substr(available_at, 20, 1) = '.' AND substr(available_at, 30, 1) = 'Z'),
    lease_owner TEXT,
    lease_token TEXT,
    lease_until TEXT CHECK (lease_until IS NULL OR (length(lease_until) = 30 AND substr(lease_until, 11, 1) = 'T' AND substr(lease_until, 20, 1) = '.' AND substr(lease_until, 30, 1) = 'Z')),
    error_code TEXT,
    error_message TEXT,
    error_retryable INTEGER CHECK (error_retryable IS NULL OR error_retryable IN (0, 1)),
    created_at TEXT NOT NULL,
    started_at TEXT,
    finished_at TEXT,
    updated_at TEXT NOT NULL,
    UNIQUE (job_id, name),
    CHECK (
        (status = 'running' AND lease_owner IS NOT NULL AND lease_token IS NOT NULL AND lease_until IS NOT NULL)
        OR
        (status <> 'running' AND lease_owner IS NULL AND lease_token IS NULL AND lease_until IS NULL)
    )
);

CREATE TABLE inbox_messages (
    id TEXT PRIMARY KEY,
    version INTEGER NOT NULL CHECK (version = 1),
    producer TEXT NOT NULL,
    message_type TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    payload BLOB NOT NULL,
    payload_hash TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending', 'claimed', 'processed', 'dead')),
    lease_owner TEXT,
    lease_token TEXT,
    lease_until TEXT CHECK (lease_until IS NULL OR (length(lease_until) = 30 AND substr(lease_until, 11, 1) = 'T' AND substr(lease_until, 20, 1) = '.' AND substr(lease_until, 30, 1) = 'Z')),
    attempt INTEGER NOT NULL DEFAULT 0 CHECK (attempt >= 0),
    available_at TEXT NOT NULL CHECK (length(available_at) = 30 AND substr(available_at, 11, 1) = 'T' AND substr(available_at, 20, 1) = '.' AND substr(available_at, 30, 1) = 'Z'),
    last_error TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (producer, idempotency_key),
    CHECK (
        (status = 'claimed' AND lease_owner IS NOT NULL AND lease_token IS NOT NULL AND lease_until IS NOT NULL)
        OR
        (status <> 'claimed' AND lease_owner IS NULL AND lease_token IS NULL AND lease_until IS NULL)
    )
);

CREATE TABLE outbox_messages (
    id TEXT PRIMARY KEY,
    job_id TEXT REFERENCES job_runs(id),
    step_id TEXT REFERENCES step_runs(id),
    version INTEGER NOT NULL CHECK (version = 1),
    destination TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    payload BLOB NOT NULL,
    payload_hash TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending', 'claimed', 'acked', 'dead')),
    lease_owner TEXT,
    lease_token TEXT,
    lease_until TEXT CHECK (lease_until IS NULL OR (length(lease_until) = 30 AND substr(lease_until, 11, 1) = 'T' AND substr(lease_until, 20, 1) = '.' AND substr(lease_until, 30, 1) = 'Z')),
    attempt INTEGER NOT NULL DEFAULT 0 CHECK (attempt >= 0),
    available_at TEXT NOT NULL CHECK (length(available_at) = 30 AND substr(available_at, 11, 1) = 'T' AND substr(available_at, 20, 1) = '.' AND substr(available_at, 30, 1) = 'Z'),
    last_error TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (destination, idempotency_key),
    CHECK (
        (status = 'claimed' AND lease_owner IS NOT NULL AND lease_token IS NOT NULL AND lease_until IS NOT NULL)
        OR
        (status <> 'claimed' AND lease_owner IS NULL AND lease_token IS NULL AND lease_until IS NULL)
    )
);

CREATE INDEX idx_step_runs_due ON step_runs(status, available_at, lease_until);
CREATE INDEX idx_step_runs_job ON step_runs(job_id, status);
CREATE INDEX idx_inbox_messages_due ON inbox_messages(status, available_at, lease_until);
CREATE INDEX idx_outbox_messages_due ON outbox_messages(status, available_at, lease_until);
CREATE INDEX idx_outbox_messages_job ON outbox_messages(job_id, status);
CREATE INDEX idx_outbox_messages_step ON outbox_messages(step_id, status);
