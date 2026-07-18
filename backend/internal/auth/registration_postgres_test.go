package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/kkkzbh/AscendAny/backend/internal/pintia"
)

type registrationRowFunc func(...any) error

func (scan registrationRowFunc) Scan(destinations ...any) error { return scan(destinations...) }

type registrationPostgresTx struct {
	now                    time.Time
	currentStudent         *string
	currentNickname        *string
	currentExporterName    *string
	currentExporterVersion *string
	usernameUnavailable    bool
	identityUnavailable    bool
	queryCount             int
	queries                []string
	executed               []string
	executedArguments      [][]any
	committed              bool
	rolledBack             bool
}

func (tx *registrationPostgresTx) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	tx.queryCount++
	tx.queries = append(tx.queries, sql)
	switch tx.queryCount {
	case 1:
		return registrationRowFunc(func(destinations ...any) error {
			*destinations[0].(*time.Time) = tx.now
			return nil
		})
	case 2:
		return registrationRowFunc(func(destinations ...any) error {
			*destinations[0].(*int64) = 71
			*destinations[1].(**string) = tx.currentStudent
			*destinations[2].(**string) = tx.currentNickname
			*destinations[3].(**string) = tx.currentExporterName
			*destinations[4].(**string) = tx.currentExporterVersion
			return nil
		})
	case 3:
		return boolRow(tx.usernameUnavailable)
	case 4:
		return boolRow(tx.identityUnavailable)
	case 5:
		return int64Row(81)
	case 6:
		return int64Row(91)
	default:
		return registrationRowFunc(func(...any) error { return errors.New("unexpected registration query") })
	}
}

func (tx *registrationPostgresTx) Exec(_ context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
	tx.executed = append(tx.executed, sql)
	tx.executedArguments = append(tx.executedArguments, append([]any(nil), arguments...))
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}

func (tx *registrationPostgresTx) Commit(context.Context) error {
	tx.committed = true
	return nil
}

func (tx *registrationPostgresTx) Rollback(context.Context) error {
	tx.rolledBack = true
	return nil
}

func TestPostgresRegistrationVerifiesCurrentParticipantAndCreatesOneAtomicSession(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 18, 5, 0, 0, 0, time.UTC)
	studentNumber := "20260001"
	ptaNickname := "Alice"
	exporterName := pintia.ExporterName
	exporterVersion := "2.2.3"
	tx := &registrationPostgresTx{
		now: now, currentStudent: &studentNumber, currentNickname: &ptaNickname,
		currentExporterName: &exporterName, currentExporterVersion: &exporterVersion,
	}
	repository, err := newPostgresRepository(func(context.Context) (postgresTx, error) { return tx, nil })
	if err != nil {
		t.Fatal(err)
	}
	result, err := repository.RegisterStudent(context.Background(), testRegisterStudentCommand(now, studentNumber, ptaNickname))
	if err != nil || result.Status != StudentRegistered || !result.AuthenticatedAt.Equal(now) {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if !tx.committed || tx.rolledBack || tx.queryCount != 6 {
		t.Fatalf("transaction committed=%v rolledBack=%v queries=%d", tx.committed, tx.rolledBack, tx.queryCount)
	}
	joinedQueries := strings.Join(tx.queries, "\n")
	for _, required := range []string{
		"logical_exams", "active_snapshot_id", "ORDER BY exam.updated_at DESC, exam.exam_id DESC",
		"auth_enrollment_grants", "INSERT INTO ascendany.auth_accounts", "INSERT INTO ascendany.auth_sessions",
	} {
		if !strings.Contains(joinedQueries, required) {
			t.Fatalf("registration queries missing %q:\n%s", required, joinedQueries)
		}
	}
	joinedExec := strings.Join(tx.executed, "\n")
	for _, required := range []string{"pg_advisory_xact_lock", "auth_refresh_tokens", "audit_events"} {
		if !strings.Contains(joinedExec, required) {
			t.Fatalf("registration transaction missing %q:\n%s", required, joinedExec)
		}
	}
	if strings.Count(joinedExec, "pg_advisory_xact_lock") != 2 || len(tx.executedArguments) < 2 ||
		len(tx.executedArguments[0]) != 1 || tx.executedArguments[0][0] != studentAccountProvisioningAdvisoryLock ||
		len(tx.executedArguments[1]) != 1 || tx.executedArguments[1][0] != pintia.ParticipantIdentityAdvisoryLockID {
		t.Fatalf("registration advisory locks = %#v/%#v", tx.executed, tx.executedArguments)
	}
	if !strings.Contains(joinedQueries, "pta_nickname") {
		t.Fatalf("registration account insert does not persist PTA nickname:\n%s", joinedQueries)
	}
}

func TestPostgresRegistrationRejectsSupersededOrConflictingIdentityBeforeInsert(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 18, 6, 0, 0, 0, time.UTC)
	studentNumber := "20260001"
	wantedNickname := "Alice"
	otherNickname := "Alice-old"
	exporterName := pintia.ExporterName
	capableVersion := "2.2.3"
	oldVersion := "2.2.2"
	incompatibleVersion := "3.0.0"
	malformedVersion := "2.02.3"
	for _, test := range []struct {
		name                string
		currentNickname     *string
		exporterVersion     *string
		usernameUnavailable bool
		identityUnavailable bool
		wantStatus          RegisterStudentStatus
	}{
		{name: "current nickname mismatch", currentNickname: &otherNickname, exporterVersion: &capableVersion, wantStatus: RegistrationIdentityUnavailable},
		{name: "old exporter semantics", currentNickname: &wantedNickname, exporterVersion: &oldVersion, wantStatus: RegistrationIdentityUnavailable},
		{name: "future incompatible exporter", currentNickname: &wantedNickname, exporterVersion: &incompatibleVersion, wantStatus: RegistrationIdentityUnavailable},
		{name: "malformed exporter", currentNickname: &wantedNickname, exporterVersion: &malformedVersion, wantStatus: RegistrationIdentityUnavailable},
		{name: "username reserved", currentNickname: &wantedNickname, exporterVersion: &capableVersion, usernameUnavailable: true, wantStatus: RegistrationUsernameUnavailable},
		{name: "actor already bound", currentNickname: &wantedNickname, exporterVersion: &capableVersion, identityUnavailable: true, wantStatus: RegistrationIdentityUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			tx := &registrationPostgresTx{
				now: now, currentStudent: &studentNumber, currentNickname: test.currentNickname,
				currentExporterName: &exporterName, currentExporterVersion: test.exporterVersion,
				usernameUnavailable: test.usernameUnavailable, identityUnavailable: test.identityUnavailable,
			}
			repository, err := newPostgresRepository(func(context.Context) (postgresTx, error) { return tx, nil })
			if err != nil {
				t.Fatal(err)
			}
			result, err := repository.RegisterStudent(context.Background(), testRegisterStudentCommand(now, studentNumber, wantedNickname))
			if err != nil || result.Status != test.wantStatus || !tx.committed {
				t.Fatalf("result=%#v committed=%v err=%v", result, tx.committed, err)
			}
			if strings.Contains(strings.Join(tx.queries, "\n"), "INSERT INTO ascendany.auth_accounts") {
				t.Fatalf("rejected registration inserted an account: %#v", tx.queries)
			}
		})
	}
}

func testRegisterStudentCommand(now time.Time, studentNumber, ptaNickname string) RegisterStudentCommand {
	return RegisterStudentCommand{
		Account: AccountRecord{
			Account: Account{
				ID: "123e4567-e89b-42d3-a456-426614174090", Username: "alice_01", DisplayName: "alice_01",
				StudentNumber: &studentNumber, PTANickname: &ptaNickname, Role: RoleStudent, AuthRevision: 1,
			},
			PasswordPHC: "$argon2id$v=19$m=19456,t=2,p=1$aaaaaaaaaaaaaaaaaaaaaa$aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		SessionID: "123e4567-e89b-42d3-a456-426614174091",
		RefreshToken: NewRefreshToken{
			ID:           "123e4567-e89b-42d3-a456-426614174092",
			SecretDigest: [32]byte{1}, CSRFDigest: [32]byte{2},
			CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour),
		},
		Now: now, SessionExpiry: now.Add(24 * time.Hour),
	}
}
