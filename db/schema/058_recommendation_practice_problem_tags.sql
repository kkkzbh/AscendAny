CREATE TABLE IF NOT EXISTS ascendany.recommendation_practice_problem_tags (
    practice_problem_id TEXT NOT NULL,
    knowledge_point TEXT NOT NULL,
    source TEXT NOT NULL DEFAULT 'legacy_problem_knowledge_mapping',
    confidence NUMERIC NOT NULL DEFAULT 1.0 CHECK (confidence >= 0 AND confidence <= 1),
    source_hash TEXT NOT NULL,
    meta JSONB NOT NULL DEFAULT '{}'::jsonb,
    imported_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (practice_problem_id, knowledge_point)
);

CREATE INDEX IF NOT EXISTS recommendation_practice_problem_tags_knowledge_idx
    ON ascendany.recommendation_practice_problem_tags (knowledge_point, practice_problem_id);
