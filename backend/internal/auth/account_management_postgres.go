package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/jackc/pgx/v5"
)

type lockedManagementPrincipal struct {
	accountDatabaseID int64
	sessionDatabaseID int64
	account           Account
	session           SessionRecord
	disabledAt        *time.Time
}

func (r *PostgresRepository) UpdateProfile(
	ctx context.Context,
	command UpdateProfileCommand,
) (UpdateProfileResult, error) {
	if err := validateManagementCommand(command.Authenticated, command.Now); err != nil {
		return UpdateProfileResult{}, err
	}
	canonicalDisplayName, err := validateTrimmedField(
		"Display name",
		command.DisplayName,
		MinDisplayNameBytes,
		MaxDisplayNameBytes,
	)
	if err != nil || canonicalDisplayName != command.DisplayName {
		return UpdateProfileResult{}, authError(ErrorInternal, "Profile display name is not canonical.", err)
	}
	result := UpdateProfileResult{Status: AccountMutationPrincipalRejected}
	err = r.transaction(ctx, "update account profile", func(tx postgresTx) error {
		locked, found, err := lockManagementPrincipal(ctx, tx, command.Authenticated, command.Now)
		if err != nil || !found {
			return err
		}
		if locked.account.DisplayName != command.DisplayName {
			tag, err := tx.Exec(ctx, `
UPDATE ascendany.auth_accounts
SET display_name = $2,
    updated_at = $3
WHERE account_id = $1`, locked.accountDatabaseID, command.DisplayName, command.Now)
			if err != nil {
				return databaseFailure("update account display name", err)
			}
			if tag.RowsAffected() != 1 {
				return databaseFailure("update account display name", errors.New("locked account was not updated"))
			}
			if err := appendManagementAuditEvent(
				ctx,
				tx,
				locked.accountDatabaseID,
				locked.sessionDatabaseID,
				"auth.profile_updated",
				command.Now,
				map[string]any{"displayName": command.DisplayName},
			); err != nil {
				return err
			}
			locked.account.DisplayName = command.DisplayName
		}
		result = UpdateProfileResult{Status: AccountMutationApplied, Account: locked.account}
		return nil
	})
	return result, err
}

func (r *PostgresRepository) ListSessions(
	ctx context.Context,
	query ListSessionsQuery,
) (ListSessionsResult, error) {
	if err := validateManagementCommand(query.Authenticated, query.Now); err != nil {
		return ListSessionsResult{}, err
	}
	if query.Limit < 1 || query.Limit > MaxListedSessions {
		return ListSessionsResult{}, authError(ErrorInternal, "Session-list limit is invalid.", nil)
	}
	result := ListSessionsResult{Status: AccountMutationPrincipalRejected}
	err := r.transaction(ctx, "list account sessions", func(tx postgresTx) error {
		locked, found, err := lockManagementPrincipal(ctx, tx, query.Authenticated, query.Now)
		if err != nil || !found {
			return err
		}
		var encoded string
		if err := tx.QueryRow(ctx, `
SELECT COALESCE(jsonb_agg(jsonb_build_object(
           'id', listed.public_id::text,
           'createdAt', listed.created_at,
           'expiresAt', listed.expires_at,
           'lastSeenAt', listed.last_seen_at,
           'revokedAt', listed.revoked_at,
           'revocationReason', listed.revocation_reason
       ) ORDER BY listed.is_current DESC, listed.created_at DESC, listed.session_id DESC), '[]'::jsonb)::text
FROM (
    SELECT session_id,
           public_id,
           created_at,
           expires_at,
           last_seen_at,
           revoked_at,
           revocation_reason,
           public_id = $3::uuid AS is_current
    FROM ascendany.auth_sessions
    WHERE account_id = $1
    ORDER BY (public_id = $3::uuid) DESC, created_at DESC, session_id DESC
    LIMIT $2
) AS listed`, locked.accountDatabaseID, query.Limit, query.Authenticated.Principal.SessionID).Scan(&encoded); err != nil {
			return databaseFailure("load account sessions", err)
		}
		sessions, err := decodeManagedSessions([]byte(encoded), query.Authenticated.Principal.SessionID, query.Now)
		if err != nil {
			return authError(ErrorInternal, "Stored session state is invalid.", err)
		}
		result = ListSessionsResult{Status: AccountMutationApplied, Sessions: sessions}
		return nil
	})
	return result, err
}

func (r *PostgresRepository) RevokeSession(
	ctx context.Context,
	command RevokeSessionCommand,
) (AccountMutationStatus, error) {
	if err := validateManagementCommand(command.Authenticated, command.Now); err != nil {
		return 0, err
	}
	if _, err := parseUUIDv4(command.TargetID); err != nil {
		return 0, authError(ErrorInternal, "Session revocation target is invalid.", err)
	}
	status := AccountMutationPrincipalRejected
	err := r.transaction(ctx, "revoke account session", func(tx postgresTx) error {
		locked, found, err := lockManagementPrincipal(ctx, tx, command.Authenticated, command.Now)
		if err != nil || !found {
			return err
		}
		var targetDatabaseID int64
		var revokedAt *time.Time
		err = tx.QueryRow(ctx, `
SELECT session_id, revoked_at
FROM ascendany.auth_sessions
WHERE account_id = $1
  AND public_id = $2::uuid
FOR UPDATE`, locked.accountDatabaseID, command.TargetID).Scan(&targetDatabaseID, &revokedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			status = AccountMutationTargetMissing
			return nil
		}
		if err != nil {
			return databaseFailure("lock session revocation target", err)
		}
		if revokedAt == nil {
			tag, err := tx.Exec(ctx, `
UPDATE ascendany.auth_sessions
SET revoked_at = $2,
    revocation_reason = 'user_revoked'
WHERE session_id = $1
  AND revoked_at IS NULL`, targetDatabaseID, command.Now)
			if err != nil {
				return databaseFailure("revoke account session", err)
			}
			if tag.RowsAffected() != 1 {
				return databaseFailure("revoke account session", errors.New("locked session was not revoked"))
			}
			if _, err := tx.Exec(ctx, `
UPDATE ascendany.auth_refresh_tokens
SET revoked_at = COALESCE(revoked_at, $2)
WHERE session_id = $1`, targetDatabaseID, command.Now); err != nil {
				return databaseFailure("revoke account session refresh family", err)
			}
			if err := appendManagementAuditEvent(
				ctx,
				tx,
				locked.accountDatabaseID,
				locked.sessionDatabaseID,
				"auth.session_revoked",
				command.Now,
				map[string]any{"targetSessionId": command.TargetID},
			); err != nil {
				return err
			}
		}
		status = AccountMutationApplied
		return nil
	})
	return status, err
}

func validateManagementCommand(authenticated AuthenticatedAccount, now time.Time) error {
	if err := validateAuthenticatedAccount(authenticated); err != nil {
		return err
	}
	if now.IsZero() || now.Location() != time.UTC {
		return authError(ErrorInternal, "Account-management timestamp must use UTC.", nil)
	}
	return nil
}

func lockManagementPrincipal(
	ctx context.Context,
	tx postgresTx,
	authenticated AuthenticatedAccount,
	now time.Time,
) (lockedManagementPrincipal, bool, error) {
	var locked lockedManagementPrincipal
	err := tx.QueryRow(ctx, `
SELECT account.account_id,
       account.public_id::text,
       account.username,
       account.display_name,
       account.student_number,
       account.role,
       account.auth_revision,
       account.disabled_at,
       session.session_id,
       session.public_id::text,
       session.auth_revision,
       session.created_at,
       session.expires_at,
       session.last_seen_at,
       session.revoked_at
FROM ascendany.auth_accounts AS account
JOIN ascendany.auth_sessions AS session
  ON session.account_id = account.account_id
WHERE account.public_id = $1::uuid
  AND session.public_id = $2::uuid
FOR UPDATE OF account, session`, authenticated.Account.ID, authenticated.Principal.SessionID).Scan(
		&locked.accountDatabaseID,
		&locked.account.ID,
		&locked.account.Username,
		&locked.account.DisplayName,
		&locked.account.StudentNumber,
		&locked.account.Role,
		&locked.account.AuthRevision,
		&locked.disabledAt,
		&locked.sessionDatabaseID,
		&locked.session.ID,
		&locked.session.AuthRevision,
		&locked.session.CreatedAt,
		&locked.session.ExpiresAt,
		&locked.session.LastSeenAt,
		&locked.session.RevokedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return lockedManagementPrincipal{}, false, nil
	}
	if err != nil {
		return lockedManagementPrincipal{}, false, databaseFailure("lock account-management principal", err)
	}
	locked.session.DatabaseID = locked.sessionDatabaseID
	locked.session.AccountID = locked.account.ID
	principal := authenticated.Principal
	active := locked.accountDatabaseID > 0 && locked.sessionDatabaseID > 0 && locked.disabledAt == nil &&
		locked.session.RevokedAt == nil && now.Before(locked.session.ExpiresAt) &&
		locked.account.ID == principal.AccountID && locked.session.ID == principal.SessionID &&
		locked.account.Role == principal.Role && locked.account.AuthRevision == principal.AuthRevision &&
		locked.session.AuthRevision == locked.account.AuthRevision
	if !active {
		return lockedManagementPrincipal{}, false, nil
	}
	return locked, true, nil
}

type storedManagedSession struct {
	ID               string     `json:"id"`
	CreatedAt        time.Time  `json:"createdAt"`
	ExpiresAt        time.Time  `json:"expiresAt"`
	LastSeenAt       time.Time  `json:"lastSeenAt"`
	RevokedAt        *time.Time `json:"revokedAt"`
	RevocationReason *string    `json:"revocationReason"`
}

func decodeManagedSessions(data []byte, currentID string, now time.Time) ([]ManagedSession, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var stored []storedManagedSession
	if err := decoder.Decode(&stored); err != nil {
		return nil, fmt.Errorf("decode sessions: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, err
	}
	sessions := make([]ManagedSession, len(stored))
	seen := make(map[string]struct{}, len(stored))
	currentCount := 0
	for index, item := range stored {
		if _, err := parseUUIDv4(item.ID); err != nil {
			return nil, fmt.Errorf("session %d ID: %w", index, err)
		}
		if _, exists := seen[item.ID]; exists {
			return nil, fmt.Errorf("session %d duplicates ID %s", index, item.ID)
		}
		seen[item.ID] = struct{}{}
		if item.CreatedAt.IsZero() || !item.ExpiresAt.After(item.CreatedAt) ||
			item.LastSeenAt.Before(item.CreatedAt) || !item.LastSeenAt.Before(item.ExpiresAt) ||
			(item.RevokedAt == nil) != (item.RevocationReason == nil) ||
			(item.RevokedAt != nil && item.RevokedAt.Before(item.CreatedAt)) {
			return nil, fmt.Errorf("session %d timestamps or revocation state are invalid", index)
		}
		createdAt := item.CreatedAt.UTC()
		expiresAt := item.ExpiresAt.UTC()
		lastSeenAt := item.LastSeenAt.UTC()
		var revokedAt *time.Time
		if item.RevokedAt != nil {
			value := item.RevokedAt.UTC()
			revokedAt = &value
		}
		current := item.ID == currentID
		if current {
			currentCount++
		}
		sessions[index] = ManagedSession{
			ID:               item.ID,
			CreatedAt:        createdAt,
			ExpiresAt:        expiresAt,
			LastSeenAt:       lastSeenAt,
			RevokedAt:        revokedAt,
			RevocationReason: item.RevocationReason,
			Current:          current,
			Active:           revokedAt == nil && now.Before(expiresAt),
		}
	}
	if currentCount != 1 {
		return nil, fmt.Errorf("session list contains %d current sessions", currentCount)
	}
	return sessions, nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing any
	err := decoder.Decode(&trailing)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("session JSON contains a trailing value")
	}
	return fmt.Errorf("decode trailing session JSON: %w", err)
}

func appendManagementAuditEvent(
	ctx context.Context,
	tx postgresTx,
	accountDatabaseID int64,
	sessionDatabaseID int64,
	eventType string,
	now time.Time,
	payload map[string]any,
) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return authError(ErrorInternal, "Account-management audit payload is invalid.", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO ascendany.audit_events (
    account_id,
    session_id,
    event_type,
    occurred_at,
    payload
)
VALUES ($1, $2, $3, $4, $5::jsonb)`, accountDatabaseID, sessionDatabaseID, eventType, now, string(encoded)); err != nil {
		return databaseFailure("append account-management audit event", err)
	}
	return nil
}
