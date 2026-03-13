CREATE TYPE account_status AS ENUM (
    'current',
    'delinquent',
    'charged_off',
    'settled',
    'closed'
);

CREATE TABLE accounts (
    id                BIGSERIAL PRIMARY KEY,
    consumer_id       BIGINT NOT NULL,
    original_creditor TEXT NOT NULL,
    account_number    TEXT NOT NULL,
    balance_due       NUMERIC(12, 2) NOT NULL,
    days_past_due     INT NOT NULL DEFAULT 0,
    status            account_status NOT NULL DEFAULT 'current',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_accounts_consumer_id ON accounts(consumer_id);
