ALTER TABLE ascendany.user_accounts
    ADD COLUMN IF NOT EXISTS display_name TEXT;

UPDATE ascendany.user_accounts
SET display_name = username
WHERE display_name IS NULL OR BTRIM(display_name) = '';

ALTER TABLE ascendany.user_accounts
    ALTER COLUMN display_name SET NOT NULL;

ALTER TABLE ascendany.user_accounts
    DROP CONSTRAINT IF EXISTS user_accounts_display_name_not_blank;
ALTER TABLE ascendany.user_accounts
    ADD CONSTRAINT user_accounts_display_name_not_blank
    CHECK (BTRIM(display_name) <> '');

ALTER TABLE ascendany.user_accounts
    ADD COLUMN IF NOT EXISTS display_name_normalized TEXT GENERATED ALWAYS AS (lower(BTRIM(display_name))) STORED;

CREATE UNIQUE INDEX IF NOT EXISTS user_accounts_display_name_normalized_uniq
    ON ascendany.user_accounts (display_name_normalized);
