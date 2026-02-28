CREATE TABLE IF NOT EXISTS ascendany.student_activity_counters (
    student_id BIGINT PRIMARY KEY REFERENCES ascendany.students(student_id) ON DELETE CASCADE,
    ai_dialogue_count BIGINT NOT NULL DEFAULT 0 CHECK (ai_dialogue_count >= 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS student_activity_counters_updated_at_idx
    ON ascendany.student_activity_counters (updated_at DESC);
