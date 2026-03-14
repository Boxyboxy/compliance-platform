CREATE TABLE contact_attempts (
    id                BIGSERIAL PRIMARY KEY,
    consumer_id       BIGINT NOT NULL,
    account_id        BIGINT NOT NULL,
    channel           TEXT NOT NULL CHECK (channel IN ('sms','email','voice')),
    status            TEXT NOT NULL DEFAULT 'pending'
                      CHECK (status IN ('pending','blocked','sent','delivered','failed')),
    block_reason      TEXT,
    workflow_id       TEXT,
    message_content   TEXT,
    compliance_result JSONB,
    scorecard_result  JSONB,
    attempted_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at      TIMESTAMPTZ
);

CREATE INDEX idx_contact_consumer_time ON contact_attempts(consumer_id, attempted_at);
