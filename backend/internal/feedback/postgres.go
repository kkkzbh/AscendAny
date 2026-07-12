package feedback

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/principalguard"
)

type PgxBeginner interface {
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
}

type feedbackTx interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	QueryRow(context.Context, string, ...any) pgx.Row
	Commit(context.Context) error
	Rollback(context.Context) error
}

type beginTransaction func(context.Context, pgx.TxOptions) (feedbackTx, error)

type PostgresRepository struct {
	begin beginTransaction
}

func NewPostgresRepository(pool PgxBeginner) (*PostgresRepository, error) {
	if pool == nil {
		return nil, feedbackError(ErrorInvalidConfiguration, true, "construct feedback PostgreSQL repository", errors.New("database pool is required"))
	}
	return &PostgresRepository{begin: func(ctx context.Context, options pgx.TxOptions) (feedbackTx, error) {
		return pool.BeginTx(ctx, options)
	}}, nil
}

func newPostgresRepository(begin beginTransaction) (*PostgresRepository, error) {
	if begin == nil {
		return nil, feedbackError(ErrorInvalidConfiguration, true, "construct feedback PostgreSQL repository", errors.New("transaction beginner is required"))
	}
	return &PostgresRepository{begin: begin}, nil
}

func (repository *PostgresRepository) SubmitAuthenticated(ctx context.Context, command SubmitCommand) (SubmitResult, error) {
	var result SubmitResult
	err := repository.transaction(ctx, "submit authenticated feedback", func(tx feedbackTx) error {
		resolved, err := principalguard.ResolveForUpdate(
			ctx,
			tx,
			command.Principal,
			principalguard.Roles(auth.RoleAdmin, auth.RoleStudent),
		)
		if err != nil {
			return mapPrincipalError("authorize authenticated feedback", err)
		}

		existing, found, err := loadExistingSubmission(ctx, tx, command, resolved.AccountDatabaseID)
		if err != nil {
			return err
		}
		if found {
			result = SubmitResult{Submission: existing, Created: false}
			return nil
		}

		windowMilliseconds := command.Policy.Window.Milliseconds()
		var recentCount int
		if err := tx.QueryRow(ctx, `
SELECT count(*)
FROM ascendany.feedback_submissions
WHERE rate_limit_subject_digest = $1
  AND created_at >= clock_timestamp() - ($2::bigint * interval '1 millisecond')`, command.SubjectDigest[:], windowMilliseconds).Scan(&recentCount); err != nil {
			return databaseFailure("count recent feedback submissions", err)
		}
		if recentCount >= command.Policy.MaximumSubmissions {
			return feedbackError(ErrorRateLimited, true, "enforce feedback rate limit", errors.New("feedback submission rate limit exceeded"))
		}

		var deliveryConfigurationVersionID int64
		if err := tx.QueryRow(ctx, `
SELECT version.configuration_version_id
FROM ascendany.configuration_items AS item
JOIN ascendany.configuration_versions AS version
  ON version.configuration_version_id = item.active_version_id
 AND version.configuration_item_id = item.configuration_item_id
WHERE item.configuration_key = $1
  AND item.configuration_kind = 'feedback_delivery'
FOR SHARE OF item`, command.Policy.DeliveryConfigurationKey).Scan(&deliveryConfigurationVersionID); errors.Is(err, pgx.ErrNoRows) {
			return feedbackError(ErrorDeliveryUnavailable, true, "resolve feedback delivery configuration", errors.New("active feedback delivery configuration is unavailable"))
		} else if err != nil {
			return databaseFailure("resolve feedback delivery configuration", err)
		}

		var feedbackDatabaseID int64
		var createdAt time.Time
		if err := tx.QueryRow(ctx, `
INSERT INTO ascendany.feedback_submissions (
    public_id,
    submission_mode,
    account_id,
    session_id,
    rate_limit_subject_digest,
    client_request_id,
    title,
    content,
    platform,
    app_version,
    user_agent
)
VALUES ($1::uuid, 'authenticated', $2, $3, $4, $5::uuid, $6, $7, $8, $9, $10)
RETURNING feedback_id, created_at`,
			command.FeedbackPublicID,
			resolved.AccountDatabaseID,
			resolved.SessionDatabaseID,
			command.SubjectDigest[:],
			command.ClientRequestID,
			command.Title,
			command.Content,
			command.Platform,
			command.AppVersion,
			command.UserAgent,
		).Scan(&feedbackDatabaseID, &createdAt); err != nil {
			return databaseFailure("insert feedback submission", err)
		}

		var deliveryDatabaseID int64
		if err := tx.QueryRow(ctx, `
INSERT INTO ascendany.feedback_delivery_jobs (
    public_id,
    feedback_id,
    delivery_configuration_version_id,
    status
)
VALUES ($1::uuid, $2, $3, 'queued')
RETURNING feedback_delivery_job_id`, command.DeliveryPublicID, feedbackDatabaseID, deliveryConfigurationVersionID).Scan(&deliveryDatabaseID); err != nil {
			return databaseFailure("insert feedback delivery job", err)
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO ascendany.feedback_delivery_events (
    feedback_delivery_job_id,
    event_sequence,
    event_type,
    payload
)
VALUES ($1, 1, 'queued', jsonb_build_object('configurationVersionId', $2::bigint))`, deliveryDatabaseID, deliveryConfigurationVersionID); err != nil {
			return databaseFailure("append feedback delivery queued event", err)
		}
		result = SubmitResult{
			Created: true,
			Submission: Submission{
				ID:            command.FeedbackPublicID,
				DeliveryJobID: command.DeliveryPublicID,
				CreatedAt:     createdAt.UTC(),
			},
		}
		return nil
	})
	if err != nil {
		return SubmitResult{}, err
	}
	return result, nil
}

func loadExistingSubmission(
	ctx context.Context,
	tx feedbackTx,
	command SubmitCommand,
	accountDatabaseID int64,
) (Submission, bool, error) {
	var submission Submission
	var storedAccountID int64
	var title string
	var content string
	var platform *string
	var appVersion *string
	var userAgent *string
	err := tx.QueryRow(ctx, `
SELECT feedback.public_id::text,
       delivery.public_id::text,
       feedback.created_at,
       feedback.account_id,
       feedback.title,
       feedback.content,
       feedback.platform,
       feedback.app_version,
       feedback.user_agent
FROM ascendany.feedback_submissions AS feedback
JOIN ascendany.feedback_delivery_jobs AS delivery
  ON delivery.feedback_id = feedback.feedback_id
WHERE feedback.rate_limit_subject_digest = $1
  AND feedback.client_request_id = $2::uuid`, command.SubjectDigest[:], command.ClientRequestID).Scan(
		&submission.ID,
		&submission.DeliveryJobID,
		&submission.CreatedAt,
		&storedAccountID,
		&title,
		&content,
		&platform,
		&appVersion,
		&userAgent,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Submission{}, false, nil
	}
	if err != nil {
		return Submission{}, false, databaseFailure("load idempotent feedback submission", err)
	}
	if storedAccountID != accountDatabaseID || title != command.Title || content != command.Content ||
		!equalOptional(platform, command.Platform) || !equalOptional(appVersion, command.AppVersion) || !equalOptional(userAgent, command.UserAgent) {
		return Submission{}, false, feedbackError(ErrorIdempotencyConflict, true, "validate idempotent feedback submission", errors.New("client request ID was already used for different feedback"))
	}
	submission.CreatedAt = submission.CreatedAt.UTC()
	return submission, true, nil
}

func equalOptional(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func (repository *PostgresRepository) transaction(
	ctx context.Context,
	operation string,
	run func(feedbackTx) error,
) (resultErr error) {
	if ctx == nil {
		return feedbackError(ErrorInvalidInput, true, operation, errors.New("context is required"))
	}
	tx, err := repository.begin(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return databaseFailure("begin "+operation, err)
	}
	finished := false
	defer func() {
		if finished {
			return
		}
		rollbackContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if rollbackErr := tx.Rollback(rollbackContext); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			wrapped := databaseFailure("rollback "+operation, rollbackErr)
			if resultErr == nil {
				resultErr = wrapped
			} else {
				resultErr = errors.Join(resultErr, wrapped)
			}
		}
	}()
	if err := run(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return databaseFailure("commit "+operation, err)
	}
	finished = true
	return nil
}

func mapPrincipalError(operation string, err error) error {
	switch principalguard.CodeOf(err) {
	case principalguard.ErrorInvalidPrincipal:
		return feedbackError(ErrorInvalidInput, true, operation, err)
	case principalguard.ErrorRejected:
		return feedbackError(ErrorPrincipalRejected, true, operation, err)
	case principalguard.ErrorStoredData:
		return feedbackError(ErrorStoredDataInvalid, true, operation, err)
	case principalguard.ErrorCanceled:
		return feedbackError(ErrorCanceled, false, operation, err)
	case principalguard.ErrorDatabase:
		return feedbackError(ErrorDatabase, false, operation, err)
	default:
		return feedbackError(ErrorDatabase, false, operation, err)
	}
}
