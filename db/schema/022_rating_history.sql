CREATE TABLE IF NOT EXISTS ascendany.rating_history (
    exam_id BIGINT NOT NULL REFERENCES ascendany.exams(exam_id) ON DELETE CASCADE,
    student_id BIGINT NOT NULL REFERENCES ascendany.students(student_id) ON DELETE RESTRICT,
    old_rating INTEGER NOT NULL,
    delta INTEGER NOT NULL,
    new_rating INTEGER NOT NULL,
    rank INTEGER,
    seed NUMERIC,
    performance NUMERIC,
    details JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (exam_id, student_id)
);

CREATE INDEX IF NOT EXISTS rating_history_student_id_idx
    ON ascendany.rating_history (student_id);

