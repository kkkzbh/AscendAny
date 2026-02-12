CREATE TABLE IF NOT EXISTS ascendany.ingest_exam_runs (
    ingest_run_id BIGINT NOT NULL REFERENCES ascendany.ingest_runs(ingest_run_id) ON DELETE CASCADE,
    exam_id BIGINT NOT NULL REFERENCES ascendany.exams(exam_id) ON DELETE CASCADE,
    fingerprint TEXT,
    status TEXT NOT NULL,
    error_message TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (ingest_run_id, exam_id)
);

CREATE INDEX IF NOT EXISTS ingest_exam_runs_exam_id_idx
    ON ascendany.ingest_exam_runs (exam_id);

