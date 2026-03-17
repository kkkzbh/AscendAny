CREATE TABLE IF NOT EXISTS ascendany.exam_auto_analysis_cache (
    exam_id BIGINT NOT NULL REFERENCES ascendany.exams(exam_id) ON DELETE CASCADE,
    student_id BIGINT NOT NULL REFERENCES ascendany.students(student_id) ON DELETE CASCADE,
    role_id TEXT NOT NULL DEFAULT 'xiaoD',
    status TEXT NOT NULL DEFAULT 'success',
    provider_type TEXT,
    reply TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL DEFAULT 'teacher_exam',
    error_message TEXT,
    generated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (exam_id, student_id, role_id)
);

CREATE INDEX IF NOT EXISTS exam_auto_analysis_cache_exam_role_idx
    ON ascendany.exam_auto_analysis_cache (exam_id, role_id, status);

CREATE INDEX IF NOT EXISTS exam_auto_analysis_cache_student_idx
    ON ascendany.exam_auto_analysis_cache (student_id, updated_at DESC);
