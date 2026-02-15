CREATE TABLE IF NOT EXISTS ascendany.import_tasks (
    run_id TEXT PRIMARY KEY,
    task_type TEXT NOT NULL,
    status TEXT NOT NULL,
    result JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS import_tasks_created_at_idx
    ON ascendany.import_tasks (created_at DESC);
