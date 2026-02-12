CREATE TABLE IF NOT EXISTS ascendany.exam_participants (
    exam_id BIGINT NOT NULL REFERENCES ascendany.exams(exam_id) ON DELETE CASCADE,
    student_id BIGINT NOT NULL REFERENCES ascendany.students(student_id) ON DELETE RESTRICT,
    user_group TEXT,
    rank INTEGER,
    total_score NUMERIC,
    time_used_seconds INTEGER,
    solved_count INTEGER,
    absent BOOLEAN NOT NULL DEFAULT FALSE,
    meta JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (exam_id, student_id)
);

CREATE INDEX IF NOT EXISTS exam_participants_student_id_idx
    ON ascendany.exam_participants (student_id);

