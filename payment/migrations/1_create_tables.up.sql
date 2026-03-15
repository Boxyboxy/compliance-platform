CREATE TYPE plan_status AS ENUM (
    'proposed',
    'accepted',
    'active',
    'completed',
    'defaulted'
);

CREATE TABLE payment_plans (
    id               BIGSERIAL PRIMARY KEY,
    account_id       BIGINT NOT NULL,
    total_amount     NUMERIC(12, 2) NOT NULL,
    num_installments INT NOT NULL,
    installment_amt  NUMERIC(12, 2) NOT NULL,
    frequency        TEXT NOT NULL DEFAULT 'monthly',
    status           plan_status NOT NULL DEFAULT 'proposed',
    proposed_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    accepted_at      TIMESTAMPTZ,
    completed_at     TIMESTAMPTZ
);

CREATE TABLE payment_events (
    id          BIGSERIAL PRIMARY KEY,
    plan_id     BIGINT NOT NULL REFERENCES payment_plans(id),
    event_type  TEXT NOT NULL,
    amount      NUMERIC(12, 2),
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    metadata    JSONB
);

CREATE INDEX idx_payment_plans_account_id ON payment_plans(account_id);
CREATE INDEX idx_payment_events_plan_id ON payment_events(plan_id);
