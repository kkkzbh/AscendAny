CREATE TABLE IF NOT EXISTS ascendany.exam_student_metrics (
    exam_id BIGINT NOT NULL REFERENCES ascendany.exams(exam_id) ON DELETE CASCADE,
    student_id BIGINT NOT NULL REFERENCES ascendany.students(student_id) ON DELETE RESTRICT,
    knowledge SMALLINT CHECK (knowledge BETWEEN 0 AND 100),
    accuracy SMALLINT CHECK (accuracy BETWEEN 0 AND 100),
    quality SMALLINT CHECK (quality BETWEEN 0 AND 100),
    flexibility SMALLINT CHECK (flexibility BETWEEN 0 AND 100),
    proficiency SMALLINT CHECK (proficiency BETWEEN 0 AND 100),
    details JSONB NOT NULL DEFAULT '{}'::jsonb,
    computed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (exam_id, student_id)
);

CREATE INDEX IF NOT EXISTS exam_student_metrics_student_id_idx
    ON ascendany.exam_student_metrics (student_id);

