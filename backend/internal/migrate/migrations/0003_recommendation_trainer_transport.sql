CREATE TABLE ascendany.recommendation_trainer_attempt_receipts (
    trainer_attempt_receipt_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    training_run_id bigint NOT NULL,
    attempt_token uuid NOT NULL,
    agent_id text NOT NULL,
    operation text NOT NULL,
    request_sha256 text NOT NULL,
    result text NOT NULL,
    model_public_id uuid,
    runtime_construction_sha256 text,
    runtime_provenance_sha256 text,
    runtime_tree_sha256 text,
    host_capability_sha256 text,
    runtime_attestation_sha256 text,
    error_code text,
    error_detail text,
    error_retryable boolean,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT recommendation_trainer_attempt_receipts_run_fk FOREIGN KEY (training_run_id)
        REFERENCES ascendany.recommendation_training_runs (training_run_id) ON DELETE RESTRICT,
    CONSTRAINT recommendation_trainer_attempt_receipts_model_fk FOREIGN KEY (model_public_id)
        REFERENCES ascendany.recommendation_models (public_id) ON DELETE RESTRICT,
    CONSTRAINT recommendation_trainer_attempt_receipts_attempt_nonzero CHECK (
        attempt_token <> '00000000-0000-0000-0000-000000000000'::uuid
    ),
    CONSTRAINT recommendation_trainer_attempt_receipts_agent_format CHECK (
        agent_id ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$'
    ),
    CONSTRAINT recommendation_trainer_attempt_receipts_operation_valid CHECK (
        operation IN ('output', 'failure')
    ),
    CONSTRAINT recommendation_trainer_attempt_receipts_request_hash CHECK (
        request_sha256 ~ '^[0-9a-f]{64}$'
    ),
    CONSTRAINT recommendation_trainer_attempt_receipts_result_valid CHECK (
        result IN ('activated', 'superseded', 'failed', 'requeued', 'output_rejected')
    ),
    CONSTRAINT recommendation_trainer_attempt_receipts_runtime_identity_shape CHECK (
        (
            result IN ('activated', 'superseded')
            AND runtime_construction_sha256 IS NOT NULL
            AND runtime_construction_sha256 ~ '^[0-9a-f]{64}$'
            AND runtime_provenance_sha256 IS NOT NULL
            AND runtime_provenance_sha256 ~ '^[0-9a-f]{64}$'
            AND runtime_tree_sha256 IS NOT NULL
            AND runtime_tree_sha256 ~ '^[0-9a-f]{64}$'
            AND host_capability_sha256 IS NOT NULL
            AND host_capability_sha256 ~ '^[0-9a-f]{64}$'
            AND runtime_attestation_sha256 IS NOT NULL
            AND runtime_attestation_sha256 ~ '^[0-9a-f]{64}$'
        )
        OR (
            result IN ('failed', 'requeued', 'output_rejected')
            AND runtime_construction_sha256 IS NULL
            AND runtime_provenance_sha256 IS NULL
            AND runtime_tree_sha256 IS NULL
            AND host_capability_sha256 IS NULL
            AND runtime_attestation_sha256 IS NULL
        )
    ),
    CONSTRAINT recommendation_trainer_attempt_receipts_shape CHECK (
        (
            operation = 'output'
            AND result IN ('activated', 'superseded')
            AND model_public_id IS NOT NULL
            AND error_code IS NULL
            AND error_detail IS NULL
            AND error_retryable IS NULL
        )
        OR (
            operation = 'output'
            AND result = 'output_rejected'
            AND model_public_id IS NULL
            AND error_code IS NOT NULL
            AND error_code ~ '^[a-z][a-z0-9_]{0,127}$'
            AND error_detail IS NOT NULL
            AND btrim(error_detail) <> ''
            AND octet_length(error_detail) <= 2048
            AND error_retryable = false
        )
        OR (
            operation = 'failure'
            AND result IN ('failed', 'requeued')
            AND model_public_id IS NULL
            AND error_code IS NULL
            AND error_detail IS NULL
            AND error_retryable IS NULL
        )
    ),
    CONSTRAINT recommendation_trainer_attempt_receipts_attempt_unique UNIQUE (
        training_run_id,
        attempt_token
    )
);

CREATE INDEX recommendation_trainer_attempt_receipts_created_idx
ON ascendany.recommendation_trainer_attempt_receipts (created_at, trainer_attempt_receipt_id);

CREATE TRIGGER recommendation_trainer_attempt_receipts_immutable_rows
BEFORE UPDATE OR DELETE ON ascendany.recommendation_trainer_attempt_receipts
FOR EACH ROW EXECUTE FUNCTION ascendany.reject_immutable_mutation();

CREATE TRIGGER recommendation_trainer_attempt_receipts_immutable_truncate
BEFORE TRUNCATE ON ascendany.recommendation_trainer_attempt_receipts
FOR EACH STATEMENT EXECUTE FUNCTION ascendany.reject_immutable_mutation();

REVOKE ALL PRIVILEGES ON TABLE ascendany.recommendation_trainer_attempt_receipts
FROM ascendany_runtime, ascendany_backup, PUBLIC;

GRANT SELECT ON TABLE ascendany.recommendation_trainer_attempt_receipts
TO ascendany_runtime, ascendany_backup;

GRANT INSERT ON TABLE ascendany.recommendation_trainer_attempt_receipts
TO ascendany_runtime;

REVOKE ALL PRIVILEGES ON SEQUENCE ascendany.recommendation_trainer_attempt_r_trainer_attempt_receipt_id_seq
FROM ascendany_runtime, ascendany_backup, PUBLIC;

GRANT USAGE, SELECT ON SEQUENCE ascendany.recommendation_trainer_attempt_r_trainer_attempt_receipt_id_seq
TO ascendany_runtime;

GRANT SELECT ON SEQUENCE ascendany.recommendation_trainer_attempt_r_trainer_attempt_receipt_id_seq
TO ascendany_backup;
