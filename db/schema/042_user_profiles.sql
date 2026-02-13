CREATE TABLE IF NOT EXISTS ascendany.user_profiles (
    account_id BIGINT PRIMARY KEY REFERENCES ascendany.user_accounts(account_id) ON DELETE CASCADE,
    student_id TEXT,
    pta_nickname TEXT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
