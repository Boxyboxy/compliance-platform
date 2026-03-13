CREATE TABLE consumers (
    id               BIGSERIAL PRIMARY KEY,
    external_id      TEXT UNIQUE NOT NULL,
    first_name       TEXT NOT NULL,
    last_name        TEXT NOT NULL,
    phone            TEXT,
    email            TEXT,
    timezone         TEXT NOT NULL DEFAULT 'America/New_York',
    consent_status   TEXT NOT NULL DEFAULT 'granted'
                     CHECK (consent_status IN ('granted', 'revoked')),
    do_not_contact   BOOLEAN NOT NULL DEFAULT false,
    attorney_on_file BOOLEAN NOT NULL DEFAULT false,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_consumers_external_id ON consumers(external_id);
