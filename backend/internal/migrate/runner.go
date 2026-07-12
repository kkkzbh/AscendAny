package migrate

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const migrationAdvisoryLockKey int64 = 0x415343454e445632

func Up(ctx context.Context, configuration Config) error {
	definitions, err := Embedded()
	if err != nil {
		return err
	}

	connectionConfig, err := pgx.ParseConfig(configuration.DatabaseURL)
	if err != nil {
		return errors.New("parse migration database URL")
	}
	connectionConfig.Password = configuration.Password
	connectionConfig.ConnectTimeout = configuration.ConnectTimeout
	connectionConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	connectionConfig.StatementCacheCapacity = 0
	connectionConfig.DescriptionCacheCapacity = 0
	connectionConfig.RuntimeParams["application_name"] = "ascendany-migrate"

	connection, err := pgx.ConnectConfig(ctx, connectionConfig)
	if err != nil {
		return errors.New("connect to migration database")
	}
	defer func() {
		closeContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = connection.Close(closeContext)
	}()

	if err := verifyAndAssumeOwner(ctx, connection); err != nil {
		return err
	}
	return apply(ctx, connection, configuration.LockTimeout, definitions)
}

func verifyAndAssumeOwner(ctx context.Context, connection *pgx.Conn) error {
	var currentDatabase string
	var sessionUser string
	if err := connection.QueryRow(ctx, `SELECT current_database(), session_user`).Scan(&currentDatabase, &sessionUser); err != nil {
		return errors.New("verify migration database session")
	}
	if currentDatabase != databaseName {
		return fmt.Errorf("connected database must be exactly %s", databaseName)
	}
	if sessionUser != databaseLogin {
		return fmt.Errorf("authenticated database role must be exactly %s", databaseLogin)
	}
	if _, err := connection.Exec(ctx, `SET ROLE ascendany_owner`); err != nil {
		return fmt.Errorf("assume database owner role %s: %w", databaseRole, err)
	}

	var currentRole string
	if err := connection.QueryRow(ctx, `SELECT current_user`).Scan(&currentRole); err != nil {
		return errors.New("verify migration owner role")
	}
	if currentRole != databaseRole {
		return fmt.Errorf("current database role must be exactly %s", databaseRole)
	}
	return nil
}

func apply(ctx context.Context, connection *pgx.Conn, lockTimeout time.Duration, definitions []Definition) (runErr error) {
	if lockTimeout < time.Millisecond {
		return errors.New("migration lock timeout must be at least 1ms")
	}
	if err := acquireSessionLock(ctx, connection, lockTimeout); err != nil {
		return err
	}
	defer func() {
		unlockContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var unlocked bool
		unlockErr := connection.QueryRow(
			unlockContext,
			`SELECT pg_advisory_unlock($1)`,
			migrationAdvisoryLockKey,
		).Scan(&unlocked)
		if runErr == nil {
			switch {
			case unlockErr != nil:
				runErr = errors.New("release migration advisory lock")
			case !unlocked:
				runErr = errors.New("migration advisory lock was not held")
			}
		}
	}()

	historyPresent, err := historyTableExists(ctx, connection)
	if err != nil {
		return err
	}

	var history []HistoryEntry
	if historyPresent {
		if err := verifyHistoryTable(ctx, connection); err != nil {
			return err
		}
		history, err = readHistory(ctx, connection)
		if err != nil {
			return err
		}
		if len(history) == 0 {
			return errors.New("migration history table is empty; provision a fresh database")
		}
	} else {
		empty, err := targetSchemaIsEmpty(ctx, connection)
		if err != nil {
			return err
		}
		if !empty {
			return errors.New("migration history is missing from a non-empty schema")
		}
	}

	if err := ValidateHistory(history, definitions); err != nil {
		return err
	}
	for _, definition := range definitions[len(history):] {
		if err := applyOne(ctx, connection, definition); err != nil {
			return err
		}
	}

	finalHistory, err := readHistory(ctx, connection)
	if err != nil {
		return err
	}
	if err := ValidateHistory(finalHistory, definitions); err != nil {
		return err
	}
	if len(finalHistory) != len(definitions) {
		return fmt.Errorf("migration history ended at version %d, want %d", len(finalHistory), len(definitions))
	}
	return nil
}

func acquireSessionLock(ctx context.Context, connection *pgx.Conn, timeout time.Duration) error {
	var setting string
	if err := connection.QueryRow(
		ctx,
		`SELECT set_config('lock_timeout', $1, false)`,
		timeout.String(),
	).Scan(&setting); err != nil {
		return errors.New("configure migration advisory lock timeout")
	}
	if _, err := connection.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationAdvisoryLockKey); err != nil {
		return fmt.Errorf("acquire migration advisory lock within %s: %w", timeout, err)
	}
	if err := connection.QueryRow(
		ctx,
		`SELECT set_config('lock_timeout', '0', false)`,
	).Scan(&setting); err != nil {
		return errors.New("reset migration advisory lock timeout")
	}
	return nil
}

func historyTableExists(ctx context.Context, connection *pgx.Conn) (bool, error) {
	var exists bool
	if err := connection.QueryRow(
		ctx,
		`SELECT to_regclass('ascendany.schema_migrations_v2') IS NOT NULL`,
	).Scan(&exists); err != nil {
		return false, errors.New("inspect migration history table")
	}
	return exists, nil
}

func targetSchemaIsEmpty(ctx context.Context, connection *pgx.Conn) (bool, error) {
	const query = `
SELECT NOT EXISTS (
    SELECT 1
    FROM pg_class c
    JOIN pg_namespace n ON n.oid = c.relnamespace
    WHERE n.nspname = 'ascendany'
    UNION ALL
    SELECT 1
    FROM pg_proc p
    JOIN pg_namespace n ON n.oid = p.pronamespace
    WHERE n.nspname = 'ascendany'
    UNION ALL
    SELECT 1
    FROM pg_type t
    JOIN pg_namespace n ON n.oid = t.typnamespace
    WHERE n.nspname = 'ascendany'
)`
	var empty bool
	if err := connection.QueryRow(ctx, query).Scan(&empty); err != nil {
		return false, errors.New("inspect fresh migration schema")
	}
	return empty, nil
}

func verifyHistoryTable(ctx context.Context, connection *pgx.Conn) error {
	const query = `
SELECT
    c.relkind = 'r',
    pg_get_userbyid(c.relowner) = 'ascendany_owner',
    (
        SELECT string_agg(
            a.attname || ':' || format_type(a.atttypid, a.atttypmod) || ':' || a.attnotnull::text,
            ',' ORDER BY a.attnum
        ) = 'version:bigint:true,name:text:true,sha256:text:true,applied_at:timestamp with time zone:true'
        FROM pg_attribute a
        WHERE a.attrelid = c.oid
          AND a.attnum > 0
          AND NOT a.attisdropped
    ),
    (
        SELECT count(*) = 2
        FROM pg_trigger t
        WHERE t.tgrelid = c.oid
          AND NOT t.tgisinternal
          AND t.tgenabled <> 'D'
    ),
    has_table_privilege('ascendany_runtime', c.oid, 'SELECT')
        AND NOT has_table_privilege('ascendany_runtime', c.oid, 'INSERT')
        AND NOT has_table_privilege('ascendany_runtime', c.oid, 'UPDATE')
        AND NOT has_table_privilege('ascendany_runtime', c.oid, 'DELETE')
        AND NOT has_table_privilege('ascendany_runtime', c.oid, 'TRUNCATE')
        AND NOT has_table_privilege('ascendany_runtime', c.oid, 'TRIGGER')
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = 'ascendany'
  AND c.relname = 'schema_migrations_v2'`
	var ordinaryTable bool
	var ownerMatches bool
	var columnsMatch bool
	var triggerCountMatches bool
	var runtimePrivilegesMatch bool
	if err := connection.QueryRow(ctx, query).Scan(
		&ordinaryTable,
		&ownerMatches,
		&columnsMatch,
		&triggerCountMatches,
		&runtimePrivilegesMatch,
	); err != nil {
		return errors.New("verify migration history structure")
	}
	if !ordinaryTable || !ownerMatches || !columnsMatch || !triggerCountMatches || !runtimePrivilegesMatch {
		return errors.New("migration history structure or ownership drift")
	}
	return nil
}

func readHistory(ctx context.Context, connection *pgx.Conn) ([]HistoryEntry, error) {
	rows, err := connection.Query(
		ctx,
		`SELECT version, name, sha256 FROM ascendany.schema_migrations_v2 ORDER BY version`,
	)
	if err != nil {
		return nil, errors.New("read migration history")
	}
	defer rows.Close()

	history := make([]HistoryEntry, 0)
	for rows.Next() {
		var entry HistoryEntry
		if err := rows.Scan(&entry.Version, &entry.Name, &entry.SHA256); err != nil {
			return nil, errors.New("decode migration history")
		}
		history = append(history, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("iterate migration history")
	}
	return history, nil
}

func applyOne(ctx context.Context, connection *pgx.Conn, definition Definition) error {
	transaction, err := connection.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin migration %d transaction: %w", definition.Version, err)
	}
	defer func() {
		_ = transaction.Rollback(context.Background())
	}()

	if _, err := transaction.Exec(ctx, definition.SQL); err != nil {
		return fmt.Errorf("execute migration %d (%s): %w", definition.Version, definition.Name, err)
	}
	if _, err := transaction.Exec(
		ctx,
		`INSERT INTO ascendany.schema_migrations_v2 (version, name, sha256) VALUES ($1, $2, $3)`,
		definition.Version,
		definition.Name,
		definition.SHA256,
	); err != nil {
		return fmt.Errorf("record migration %d (%s): %w", definition.Version, definition.Name, err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("commit migration %d (%s): %w", definition.Version, definition.Name, err)
	}
	return nil
}
