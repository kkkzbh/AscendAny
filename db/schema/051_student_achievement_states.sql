CREATE TABLE IF NOT EXISTS ascendany.student_achievement_states (
    student_id BIGINT NOT NULL REFERENCES ascendany.students(student_id) ON DELETE CASCADE,
    achievement_code TEXT NOT NULL REFERENCES ascendany.achievement_definitions(achievement_code) ON DELETE CASCADE,
    progress_value NUMERIC NOT NULL DEFAULT 0,
    tier SMALLINT NOT NULL DEFAULT 0 CHECK (tier BETWEEN 0 AND 3),
    achieved_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (student_id, achievement_code)
);

CREATE INDEX IF NOT EXISTS student_achievement_states_achievement_tier_idx
    ON ascendany.student_achievement_states (achievement_code, tier);

CREATE INDEX IF NOT EXISTS student_achievement_states_updated_at_idx
    ON ascendany.student_achievement_states (updated_at DESC);
