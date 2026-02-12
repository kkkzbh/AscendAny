CREATE TABLE IF NOT EXISTS ascendany.student_current_metrics (
    student_id BIGINT PRIMARY KEY REFERENCES ascendany.students(student_id) ON DELETE CASCADE,
    knowledge NUMERIC CHECK (knowledge BETWEEN 0 AND 100),
    accuracy NUMERIC CHECK (accuracy BETWEEN 0 AND 100),
    quality NUMERIC CHECK (quality BETWEEN 0 AND 100),
    flexibility NUMERIC CHECK (flexibility BETWEEN 0 AND 100),
    proficiency NUMERIC CHECK (proficiency BETWEEN 0 AND 100),
    rating INTEGER NOT NULL DEFAULT 800,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

