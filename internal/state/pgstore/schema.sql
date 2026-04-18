CREATE TABLE IF NOT EXISTS agents (
    task            TEXT PRIMARY KEY,
    host_type       TEXT NOT NULL DEFAULT '',
    resume_token    TEXT NOT NULL DEFAULT '',
    workspace_path  TEXT NOT NULL DEFAULT '',
    branch_name     TEXT NOT NULL DEFAULT '',
    base_branch     TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT '',
    last_output     TEXT NOT NULL DEFAULT '',
    start_time      TIMESTAMPTZ,
    mode            TEXT NOT NULL DEFAULT '',
    current_intent  TEXT NOT NULL DEFAULT '',
    mission_id      BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS chessmaster (
    id          INTEGER PRIMARY KEY CHECK (id = 1),
    status      TEXT NOT NULL DEFAULT '',
    last_action TEXT NOT NULL DEFAULT '',
    updated_at  TIMESTAMPTZ
);
INSERT INTO chessmaster (id, status, last_action) VALUES (1, '', '')
ON CONFLICT (id) DO NOTHING;

CREATE TABLE IF NOT EXISTS messages (
    id        BIGSERIAL PRIMARY KEY,
    from_task TEXT NOT NULL,
    to_task   TEXT NOT NULL,
    content   TEXT NOT NULL,
    timestamp TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_messages_to_id ON messages (to_task, id);

CREATE TABLE IF NOT EXISTS cursors (
    task    TEXT PRIMARY KEY,
    last_id BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS missions (
    id           BIGSERIAL    PRIMARY KEY,
    status       TEXT         NOT NULL DEFAULT 'pending',
    payload      JSONB        NOT NULL,
    agent_task   TEXT,
    claimed_by   TEXT,
    claimed_at   TIMESTAMPTZ,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ,
    error        TEXT         NOT NULL DEFAULT '',
    priority     INT          NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_missions_pending
    ON missions (status, priority DESC, created_at)
    WHERE status = 'pending';

CREATE INDEX IF NOT EXISTS idx_missions_claimed
    ON missions (status, claimed_at)
    WHERE status = 'claimed';

-- objective_id on agents for display (spec-16a).
ALTER TABLE agents ADD COLUMN IF NOT EXISTS objective_id TEXT NOT NULL DEFAULT '';

-- DAG columns on missions (spec-16a).
ALTER TABLE missions ADD COLUMN IF NOT EXISTS objective_id TEXT;
ALTER TABLE missions ADD COLUMN IF NOT EXISTS parent_id    BIGINT;
ALTER TABLE missions ADD COLUMN IF NOT EXISTS depth        INT NOT NULL DEFAULT 0;
ALTER TABLE missions ADD COLUMN IF NOT EXISTS dedupe_key   TEXT NOT NULL DEFAULT '';
ALTER TABLE missions ADD COLUMN IF NOT EXISTS proposed_by  TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_missions_parent ON missions (parent_id) WHERE parent_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_missions_objective ON missions (objective_id) WHERE objective_id IS NOT NULL;
-- Dedupe: blocks duplicates only for active missions (pending, claimed).
CREATE UNIQUE INDEX IF NOT EXISTS idx_missions_dedupe
    ON missions (objective_id, dedupe_key)
    WHERE dedupe_key != '' AND status IN ('pending', 'claimed');

-- Objectives table (spec-16a).
CREATE TABLE IF NOT EXISTS objectives (
    id              TEXT PRIMARY KEY,
    description     TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'open',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by      TEXT NOT NULL DEFAULT '',
    max_missions    INT NOT NULL DEFAULT 100,
    max_depth       INT NOT NULL DEFAULT 5,
    max_runtime     BIGINT NOT NULL DEFAULT 14400000000000,  -- 4h in nanoseconds
    max_branching   INT NOT NULL DEFAULT 5,
    max_tokens      BIGINT NOT NULL DEFAULT 0,               -- reserved, not enforced
    max_graph_writes INT NOT NULL DEFAULT 0,                  -- reserved, not enforced
    mission_count   INT NOT NULL DEFAULT 0,
    finding_count   INT NOT NULL DEFAULT 0,
    tokens_used     BIGINT NOT NULL DEFAULT 0,               -- reserved, not enforced
    graph_writes    INT NOT NULL DEFAULT 0,                   -- reserved, not enforced
    outcome         TEXT NOT NULL DEFAULT '',
    outcome_data    JSONB
);

-- Proposals table (spec-16a).
CREATE TABLE IF NOT EXISTS proposals (
    id              BIGSERIAL PRIMARY KEY,
    objective_id    TEXT NOT NULL,
    parent_mission  BIGINT NOT NULL,
    proposed_by     TEXT NOT NULL DEFAULT '',
    task            TEXT NOT NULL DEFAULT '',
    contract        TEXT NOT NULL DEFAULT '',
    priority        INT NOT NULL DEFAULT 0,
    dedupe_key      TEXT NOT NULL DEFAULT '',
    rationale       TEXT NOT NULL DEFAULT '',
    evidence        JSONB,
    status          TEXT NOT NULL DEFAULT 'pending',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    reviewed_at     TIMESTAMPTZ,
    rejection_note  TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_proposals_pending
    ON proposals (status, priority DESC, created_at)
    WHERE status = 'pending';

CREATE INDEX IF NOT EXISTS idx_proposals_objective
    ON proposals (objective_id);

-- Agent lifecycle events (spec-27 observability).
CREATE TABLE IF NOT EXISTS agent_events (
    id          BIGSERIAL PRIMARY KEY,
    task        TEXT NOT NULL,
    event       TEXT NOT NULL,
    detail      TEXT,
    timestamp   TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_agent_events_task ON agent_events (task, timestamp DESC);

-- spec-28: additive columns for structured event model.
ALTER TABLE agent_events ADD COLUMN IF NOT EXISTS event_id     TEXT;
ALTER TABLE agent_events ADD COLUMN IF NOT EXISTS component    TEXT;
ALTER TABLE agent_events ADD COLUMN IF NOT EXISTS category     TEXT;
ALTER TABLE agent_events ADD COLUMN IF NOT EXISTS resource     TEXT;
ALTER TABLE agent_events ADD COLUMN IF NOT EXISTS action       TEXT;
ALTER TABLE agent_events ADD COLUMN IF NOT EXISTS outcome      TEXT;
ALTER TABLE agent_events ADD COLUMN IF NOT EXISTS severity     TEXT NOT NULL DEFAULT 'info';
ALTER TABLE agent_events ADD COLUMN IF NOT EXISTS mission_id   BIGINT NOT NULL DEFAULT 0;
ALTER TABLE agent_events ADD COLUMN IF NOT EXISTS objective_id TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_events ADD COLUMN IF NOT EXISTS attrs_json   JSONB;
-- Default existing event column for new rows that only use structured fields.
ALTER TABLE agent_events ALTER COLUMN event SET DEFAULT '';

-- Staging runs (spec-26: async strategy staging).
CREATE TABLE IF NOT EXISTS staging_runs (
    id                   TEXT PRIMARY KEY,
    strategy_name        TEXT NOT NULL,
    params_json          JSONB NOT NULL,
    params_fp            TEXT NOT NULL,
    status               TEXT NOT NULL DEFAULT 'created',
    error_json           JSONB,
    result_json          JSONB,
    trace_json           JSONB,
    resume_count         INT DEFAULT 0,
    recovered_at         TIMESTAMPTZ,
    execution_claimed_at TIMESTAMPTZ,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_staging_runs_status ON staging_runs(status);

CREATE TABLE IF NOT EXISTS staging_run_tables (
    run_id               TEXT NOT NULL REFERENCES staging_runs(id) ON DELETE CASCADE,
    table_name           TEXT NOT NULL,
    fetch_mode           TEXT NOT NULL,
    required             BOOLEAN NOT NULL DEFAULT TRUE,
    status               TEXT NOT NULL DEFAULT 'pending',
    partial              BOOLEAN NOT NULL DEFAULT FALSE,
    parquet_path         TEXT,
    row_count            BIGINT DEFAULT 0,
    bytes_staged         BIGINT DEFAULT 0,
    pages_completed      INT DEFAULT 0,
    total_estimate       INT DEFAULT 0,
    error_json           JSONB,
    fetch_plan_json      JSONB,
    retry_count          INT DEFAULT 0,
    last_offset          INT DEFAULT 0,
    last_cursor          TEXT,
    last_error_at        TIMESTAMPTZ,
    resumed_from_restart BOOLEAN DEFAULT FALSE,
    started_at           TIMESTAMPTZ,
    completed_at         TIMESTAMPTZ,
    PRIMARY KEY (run_id, table_name)
);
