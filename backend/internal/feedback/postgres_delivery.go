package feedback

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

func (repository *PostgresRepository) ClaimDelivery(
	ctx context.Context,
	owner string,
	attemptToken string,
	leaseDuration time.Duration,
) (claim *DeliveryClaim, resultErr error) {
	if owner == "" || !canonicalUUIDv4.MatchString(attemptToken) || leaseDuration <= 0 || leaseDuration.Milliseconds() <= 0 {
		return nil, feedbackError(ErrorInvalidInput, true, "claim feedback delivery", errors.New("owner, canonical attempt token, and positive lease are required"))
	}
	leaseMilliseconds := leaseDuration.Milliseconds()
	resultErr = repository.transaction(ctx, "claim feedback delivery", func(tx feedbackTx) error {
		candidate := DeliveryClaim{}
		var previousStatus string
		err := tx.QueryRow(ctx, `
WITH candidate AS (
    SELECT feedback_delivery_job_id, status
    FROM ascendany.feedback_delivery_jobs
    WHERE (
        status = 'queued'
        AND next_attempt_at <= clock_timestamp()
    ) OR (
        status = 'running'
        AND lease_expires_at <= clock_timestamp()
    )
    ORDER BY
        CASE WHEN status = 'queued' THEN next_attempt_at ELSE lease_expires_at END,
        feedback_delivery_job_id
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
UPDATE ascendany.feedback_delivery_jobs AS job
SET status = 'running',
    attempt_count = job.attempt_count + 1,
    attempt_token = $2::uuid,
    lease_owner = $1,
    lease_expires_at = clock_timestamp() + ($3::bigint * interval '1 millisecond'),
    started_at = COALESCE(job.started_at, clock_timestamp()),
    updated_at = clock_timestamp()
FROM candidate
WHERE job.feedback_delivery_job_id = candidate.feedback_delivery_job_id
RETURNING job.feedback_delivery_job_id,
          job.public_id::text,
          job.attempt_count,
          job.attempt_token::text,
          job.lease_owner,
          job.lease_expires_at,
          candidate.status`, owner, attemptToken, leaseMilliseconds).Scan(
			&candidate.DatabaseID,
			&candidate.ID,
			&candidate.AttemptCount,
			&candidate.AttemptToken,
			&candidate.LeaseOwner,
			&candidate.LeaseExpiresAt,
			&previousStatus,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			claim = nil
			return nil
		}
		if err != nil {
			return databaseFailure("claim feedback delivery", err)
		}
		candidate.LeaseExpiresAt = candidate.LeaseExpiresAt.UTC()
		candidate.Reclaimed = previousStatus == "running"
		eventType := "claimed"
		if candidate.Reclaimed {
			eventType = "reclaimed"
		}
		if err := appendDeliveryEvent(ctx, tx, candidate.DatabaseID, eventType, map[string]any{
			"attemptCount": candidate.AttemptCount,
			"leaseOwner":   candidate.LeaseOwner,
		}); err != nil {
			return err
		}
		claim = &candidate
		return nil
	})
	return claim, resultErr
}

func (repository *PostgresRepository) RenewDeliveryLease(
	ctx context.Context,
	claim DeliveryClaim,
	leaseDuration time.Duration,
) error {
	if err := validateDeliveryClaim(claim); err != nil {
		return err
	}
	if leaseDuration <= 0 || leaseDuration.Milliseconds() <= 0 {
		return feedbackError(ErrorInvalidInput, true, "renew feedback delivery lease", errors.New("positive lease duration is required"))
	}
	return repository.transaction(ctx, "renew feedback delivery lease", func(tx feedbackTx) error {
		var leaseExpiresAt time.Time
		err := tx.QueryRow(ctx, `
UPDATE ascendany.feedback_delivery_jobs
SET lease_expires_at = clock_timestamp() + ($5::bigint * interval '1 millisecond'),
    updated_at = clock_timestamp()
WHERE feedback_delivery_job_id = $1
  AND public_id = $2::uuid
  AND status = 'running'
  AND attempt_count = $3
  AND attempt_token = $4::uuid
  AND lease_owner = $6
  AND lease_expires_at > clock_timestamp()
RETURNING lease_expires_at`, claim.DatabaseID, claim.ID, claim.AttemptCount, claim.AttemptToken, leaseDuration.Milliseconds(), claim.LeaseOwner).Scan(&leaseExpiresAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return feedbackError(ErrorLeaseLost, false, "renew feedback delivery lease", errors.New("delivery attempt is no longer active"))
		}
		if err != nil {
			return databaseFailure("renew feedback delivery lease", err)
		}
		return nil
	})
}

func (repository *PostgresRepository) LoadDelivery(ctx context.Context, claim DeliveryClaim) (request DeliveryRequest, resultErr error) {
	if err := validateDeliveryClaim(claim); err != nil {
		return DeliveryRequest{}, err
	}
	resultErr = repository.transaction(ctx, "load feedback delivery", func(tx feedbackTx) error {
		var configurationJSON string
		var attachmentsJSON string
		err := tx.QueryRow(ctx, `
SELECT feedback.public_id::text,
       feedback.title,
       feedback.content,
       feedback.platform,
       feedback.app_version,
       feedback.user_agent,
       version.configuration_version_id,
       version.schema_id,
       version.document::text,
       version.credential_ref,
       account.public_id::text,
       account.username,
       account.display_name,
       account.student_number,
       account.pta_nickname,
       account.role,
       (
           SELECT COALESCE(
               jsonb_agg(
                   jsonb_build_object(
                       'sequence', attachment.attachment_sequence,
                       'filename', attachment.filename,
                       'sha256', artifact.sha256,
                       'sizeBytes', artifact.size_bytes,
                       'mediaType', artifact.media_type,
                       'storageKey', artifact.storage_key
                   ) ORDER BY attachment.attachment_sequence
               ),
               '[]'::jsonb
           )
           FROM ascendany.feedback_attachments AS attachment
           JOIN ascendany.artifacts AS artifact
             ON artifact.artifact_id = attachment.artifact_id
           WHERE attachment.feedback_id = feedback.feedback_id
       )::text
FROM ascendany.feedback_delivery_jobs AS job
JOIN ascendany.feedback_submissions AS feedback
  ON feedback.feedback_id = job.feedback_id
JOIN ascendany.auth_accounts AS account
  ON account.account_id = feedback.account_id
JOIN ascendany.configuration_versions AS version
  ON version.configuration_version_id = job.delivery_configuration_version_id
 AND version.configuration_kind = 'feedback_delivery'
WHERE job.feedback_delivery_job_id = $1
  AND job.public_id = $2::uuid
  AND job.status = 'running'
  AND job.attempt_count = $3
  AND job.attempt_token = $4::uuid
  AND job.lease_owner = $5
  AND job.lease_expires_at > clock_timestamp()`, claim.DatabaseID, claim.ID, claim.AttemptCount, claim.AttemptToken, claim.LeaseOwner).Scan(
			&request.FeedbackID,
			&request.Title,
			&request.Content,
			&request.Platform,
			&request.AppVersion,
			&request.UserAgent,
			&request.ConfigurationID,
			&request.ConfigurationSchema,
			&configurationJSON,
			&request.CredentialRef,
			&request.Sender.AccountID,
			&request.Sender.Username,
			&request.Sender.DisplayName,
			&request.Sender.StudentNumber,
			&request.Sender.PTANickname,
			&request.Sender.Role,
			&attachmentsJSON,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return feedbackError(ErrorLeaseLost, false, "load feedback delivery", errors.New("delivery attempt is no longer active"))
		}
		if err != nil {
			return databaseFailure("load feedback delivery", err)
		}
		request.Configuration = json.RawMessage(configurationJSON)
		var object map[string]json.RawMessage
		if !json.Valid(request.Configuration) || json.Unmarshal(request.Configuration, &object) != nil || object == nil {
			return feedbackError(ErrorStoredDataInvalid, true, "load feedback delivery", errors.New("delivery configuration is not a JSON object"))
		}
		if validateDeliverySender(request.Sender) != nil {
			return feedbackError(ErrorStoredDataInvalid, true, "load feedback delivery", errors.New("delivery sender identity is invalid"))
		}
		if !json.Valid([]byte(attachmentsJSON)) || json.Unmarshal([]byte(attachmentsJSON), &request.Attachments) != nil ||
			validateDeliveryAttachmentManifest(request.Attachments) != nil {
			return feedbackError(ErrorStoredDataInvalid, true, "load feedback delivery", errors.New("delivery attachment manifest is invalid"))
		}
		return nil
	})
	return request, resultErr
}

func (repository *PostgresRepository) CompleteDelivery(ctx context.Context, claim DeliveryClaim, receiptSHA256 string) error {
	if err := validateDeliveryClaim(claim); err != nil {
		return err
	}
	if !lowercaseSHA256.MatchString(receiptSHA256) {
		return feedbackError(ErrorInvalidInput, true, "complete feedback delivery", errors.New("receipt SHA-256 is invalid"))
	}
	return repository.transitionDelivery(ctx, claim, "complete feedback delivery", `
UPDATE ascendany.feedback_delivery_jobs
SET status = 'succeeded',
    attempt_token = NULL,
    lease_owner = NULL,
    lease_expires_at = NULL,
    provider_receipt_sha256 = $6,
    finished_at = clock_timestamp(),
    updated_at = clock_timestamp()
WHERE feedback_delivery_job_id = $1
  AND public_id = $2::uuid
  AND status = 'running'
  AND attempt_count = $3
  AND attempt_token = $4::uuid
  AND lease_owner = $5
  AND lease_expires_at > clock_timestamp()`, []any{receiptSHA256}, "succeeded", map[string]any{"receiptSha256": receiptSHA256})
}

func (repository *PostgresRepository) RequeueDelivery(
	ctx context.Context,
	claim DeliveryClaim,
	retryDelay time.Duration,
	reason string,
) error {
	if err := validateDeliveryClaim(claim); err != nil {
		return err
	}
	if retryDelay < time.Second || !providerFailureCodePattern.MatchString(reason) {
		return feedbackError(ErrorInvalidInput, true, "requeue feedback delivery", errors.New("bounded retry delay and canonical reason are required"))
	}
	return repository.transitionDelivery(ctx, claim, "requeue feedback delivery", `
UPDATE ascendany.feedback_delivery_jobs
SET status = 'queued',
    attempt_token = NULL,
    lease_owner = NULL,
    lease_expires_at = NULL,
    next_attempt_at = clock_timestamp() + ($6::bigint * interval '1 millisecond'),
    updated_at = clock_timestamp()
WHERE feedback_delivery_job_id = $1
  AND public_id = $2::uuid
  AND status = 'running'
  AND attempt_count = $3
  AND attempt_token = $4::uuid
  AND lease_owner = $5
  AND lease_expires_at > clock_timestamp()`, []any{retryDelay.Milliseconds()}, "retry_scheduled", map[string]any{
		"delayMilliseconds": retryDelay.Milliseconds(),
		"reason":            reason,
	})
}

func (repository *PostgresRepository) FailDelivery(ctx context.Context, claim DeliveryClaim, code, detail string) error {
	if err := validateDeliveryClaim(claim); err != nil {
		return err
	}
	if !providerFailureCodePattern.MatchString(code) || detail == "" || len(detail) > 4096 {
		return feedbackError(ErrorInvalidInput, true, "fail feedback delivery", errors.New("canonical code and bounded detail are required"))
	}
	return repository.transitionDelivery(ctx, claim, "fail feedback delivery", `
UPDATE ascendany.feedback_delivery_jobs
SET status = 'failed',
    attempt_token = NULL,
    lease_owner = NULL,
    lease_expires_at = NULL,
    error_code = $6,
    error_detail = $7,
    finished_at = clock_timestamp(),
    updated_at = clock_timestamp()
WHERE feedback_delivery_job_id = $1
  AND public_id = $2::uuid
  AND status = 'running'
  AND attempt_count = $3
  AND attempt_token = $4::uuid
  AND lease_owner = $5
  AND lease_expires_at > clock_timestamp()`, []any{code, detail}, "failed", map[string]any{"code": code})
}

func (repository *PostgresRepository) transitionDelivery(
	ctx context.Context,
	claim DeliveryClaim,
	operation string,
	query string,
	extraArguments []any,
	eventType string,
	payload map[string]any,
) error {
	return repository.transaction(ctx, operation, func(tx feedbackTx) error {
		arguments := []any{claim.DatabaseID, claim.ID, claim.AttemptCount, claim.AttemptToken, claim.LeaseOwner}
		arguments = append(arguments, extraArguments...)
		commandTag, err := tx.Exec(ctx, query, arguments...)
		if err != nil {
			return databaseFailure(operation, err)
		}
		if commandTag.RowsAffected() != 1 {
			return feedbackError(ErrorLeaseLost, false, operation, errors.New("delivery attempt is no longer active"))
		}
		return appendDeliveryEvent(ctx, tx, claim.DatabaseID, eventType, payload)
	})
}

func appendDeliveryEvent(ctx context.Context, tx feedbackTx, jobID int64, eventType string, payload map[string]any) error {
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return feedbackError(ErrorStoredDataInvalid, true, "encode feedback delivery event", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO ascendany.feedback_delivery_events (
    feedback_delivery_job_id,
    event_sequence,
    event_type,
    payload
)
SELECT $1,
       COALESCE(MAX(event_sequence), 0) + 1,
       $2,
       $3::jsonb
FROM ascendany.feedback_delivery_events
WHERE feedback_delivery_job_id = $1`, jobID, eventType, string(payloadJSON)); err != nil {
		return databaseFailure("append feedback delivery event", err)
	}
	return nil
}

func validateDeliveryClaim(claim DeliveryClaim) error {
	if claim.DatabaseID <= 0 || !canonicalUUIDv4.MatchString(claim.ID) || claim.AttemptCount <= 0 ||
		!canonicalUUIDv4.MatchString(claim.AttemptToken) || claim.LeaseOwner == "" {
		return feedbackError(ErrorInvalidInput, true, "validate feedback delivery claim", errors.New("canonical active delivery claim is required"))
	}
	return nil
}
