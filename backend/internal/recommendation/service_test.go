package recommendation

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/artifact"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

const (
	testAdminAccountID = "123e4567-e89b-42d3-a456-426614174001"
	testAdminSessionID = "123e4567-e89b-42d3-a456-426614174002"
	testAdminJWTID     = "123e4567-e89b-42d3-a456-426614174003"
	testRunID          = "123e4567-e89b-42d3-a456-426614174004"
	testStudentID      = "123e4567-e89b-42d3-a456-426614174011"
	testStudentSession = "123e4567-e89b-42d3-a456-426614174012"
	testStudentJWT     = "123e4567-e89b-42d3-a456-426614174013"
)

type blockingQueueRepository struct {
	dataset TrainingDataset
	entered chan QueueCommand
	release chan struct{}
}

func (repository *blockingQueueRepository) PrepareTraining(context.Context, auth.AccessPrincipal, string) (TrainingDataset, error) {
	return repository.dataset, nil
}

func (repository *blockingQueueRepository) QueueTraining(_ context.Context, command QueueCommand) (QueueResult, error) {
	repository.entered <- command
	<-repository.release
	return QueueResult{Created: true, Run: TrainingRun{ID: command.RunPublicID, InputArtifact: command.Artifact}}, nil
}

func TestQueueTrainingHoldsArtifactLockThroughDatabaseCommit(t *testing.T) {
	store, err := artifact.NewStore(filepath.Join(t.TempDir(), "artifacts"), 4<<20)
	if err != nil {
		t.Fatal(err)
	}
	repository := &blockingQueueRepository{
		dataset: testTrainingDataset(t), entered: make(chan QueueCommand, 1), release: make(chan struct{}),
	}
	service, err := newQueueService(repository, store, ServiceConfig{MaximumInputBundleBytes: 1 << 20}, func() (string, error) {
		return testRunID, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	resultChannel := make(chan error, 1)
	go func() {
		_, queueErr := service.QueueTraining(context.Background(), QueueInput{
			Principal: testAdminPrincipal(), ConfigurationKey: repository.dataset.Configuration.Key,
			ExpectedAnalyticsGenerationID: repository.dataset.Analytics.GenerationID,
			ExpectedAnalyticsHeadRevision: repository.dataset.Analytics.HeadRevision,
		})
		resultChannel <- queueErr
	}()
	command := <-repository.entered
	secondPublication := make(chan *artifact.Publication, 1)
	secondError := make(chan error, 1)
	go func() {
		publication, publishErr := store.Publish(context.Background(), bytes.NewReader(command.Bundle.CanonicalJSON))
		if publishErr != nil {
			secondError <- publishErr
			return
		}
		secondPublication <- publication
	}()
	select {
	case publication := <-secondPublication:
		_ = publication.Release()
		t.Fatal("second publication acquired the hash lock before the queue transaction returned")
	case err := <-secondError:
		t.Fatalf("second publication failed: %v", err)
	case <-time.After(75 * time.Millisecond):
	}
	close(repository.release)
	if err := <-resultChannel; err != nil {
		t.Fatal(err)
	}
	select {
	case publication := <-secondPublication:
		if err := publication.Release(); err != nil {
			t.Fatal(err)
		}
	case err := <-secondError:
		t.Fatal(err)
	case <-time.After(2 * time.Second):
		t.Fatal("second publication did not acquire the released hash lock")
	}
}

func TestQueueServiceRejectsWrongRoleBeforeArtifactPublication(t *testing.T) {
	store, err := artifact.NewStore(filepath.Join(t.TempDir(), "artifacts"), 4<<20)
	if err != nil {
		t.Fatal(err)
	}
	repository := &blockingQueueRepository{dataset: testTrainingDataset(t), entered: make(chan QueueCommand, 1), release: make(chan struct{})}
	service, err := newQueueService(repository, store, ServiceConfig{MaximumInputBundleBytes: 1 << 20}, func() (string, error) {
		return testRunID, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	principal := testAdminPrincipal()
	principal.Role = auth.RoleStudent
	if _, err := service.QueueTraining(context.Background(), QueueInput{Principal: principal, ConfigurationKey: repository.dataset.Configuration.Key}); CodeOf(err) != ErrorInvalidInput {
		t.Fatalf("error=%v code=%q", err, CodeOf(err))
	}
	select {
	case <-repository.entered:
		t.Fatal("repository was called for an invalid principal")
	default:
	}
}

type readerRepositoryFunc func(context.Context, auth.AccessPrincipal) (CurrentRecommendation, error)

func (function readerRepositoryFunc) ReadCurrent(ctx context.Context, principal auth.AccessPrincipal) (CurrentRecommendation, error) {
	return function(ctx, principal)
}

func TestReaderServiceOwnsOnlyStudentReadCapability(t *testing.T) {
	want := CurrentRecommendation{State: RecommendationUnavailable}
	service, err := NewReaderService(readerRepositoryFunc(func(_ context.Context, principal auth.AccessPrincipal) (CurrentRecommendation, error) {
		if principal != testStudentPrincipal() {
			t.Fatalf("principal = %#v", principal)
		}
		return want, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	got, err := service.ReadCurrent(context.Background(), testStudentPrincipal())
	if err != nil || got.State != want.State {
		t.Fatalf("result = %#v error = %v", got, err)
	}
	if _, err := service.ReadCurrent(context.Background(), testAdminPrincipal()); CodeOf(err) != ErrorInvalidInput {
		t.Fatalf("admin read error = %v code = %q", err, CodeOf(err))
	}
}

type principalVerifierFunc func(string) (auth.AccessPrincipal, error)

func (function principalVerifierFunc) VerifyAccessToken(token string) (auth.AccessPrincipal, error) {
	return function(token)
}

func TestApplicationCapabilitiesVerifyBeforeDispatch(t *testing.T) {
	verificationFailure := errors.New("access rejected")
	reader, err := NewReaderApplicationService(
		principalVerifierFunc(func(token string) (auth.AccessPrincipal, error) {
			if token != "student-token" {
				return auth.AccessPrincipal{}, verificationFailure
			}
			return testStudentPrincipal(), nil
		}),
		readerRepositoryFunc(func(_ context.Context, principal auth.AccessPrincipal) (CurrentRecommendation, error) {
			if principal != testStudentPrincipal() {
				t.Fatalf("principal = %#v", principal)
			}
			return CurrentRecommendation{State: RecommendationUnavailable}, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.ReadCurrent(context.Background(), "rejected"); !errors.Is(err, verificationFailure) {
		t.Fatalf("verification error = %v", err)
	}
	if result, err := reader.ReadCurrent(context.Background(), "student-token"); err != nil || result.State != RecommendationUnavailable {
		t.Fatalf("result = %#v error = %v", result, err)
	}
}

func TestRecommendationAdminReaderRejectsNonUTCTimestamps(t *testing.T) {
	t.Parallel()
	localTime := time.Date(2026, 7, 12, 12, 0, 0, 0, time.FixedZone("local", 8*60*60))
	run := TrainingRun{
		DatabaseID:                     1,
		ID:                             testRunID,
		SourceAnalyticsGenerationID:    2,
		SourceAnalyticsHeadRevision:    3,
		InputArtifact:                  artifact.Artifact{Hash: strings.Repeat("a", 64), Size: 1},
		TrainingConfigurationVersionID: 4,
		KnowledgeCatalogVersionID:      5,
		BundleProtocol:                 TrainingBundleProtocolV2,
		InputManifestSHA256:            strings.Repeat("b", 64),
		Status:                         RunQueued,
		CreatedAt:                      localTime,
	}
	if err := ValidateTrainingRunDetail(TrainingRunDetail{Run: run, TrainingConfigurationKey: "recommendation.training.default"}, testRunID); err == nil {
		t.Fatal("non-UTC training run timestamp was accepted")
	}
	events := TrainingEventPage{RunID: testRunID, Items: []TrainingEvent{{
		Sequence:  1,
		Type:      "failed",
		Payload:   []byte(`{"attemptCount":1,"code":"trainer_failed"}`),
		CreatedAt: localTime,
	}}}
	if err := ValidateTrainingEventPage(events, testRunID, 0, 100); err == nil {
		t.Fatal("non-UTC training event timestamp was accepted")
	}
}

func testAdminPrincipal() auth.AccessPrincipal {
	return auth.AccessPrincipal{
		AccountID: testAdminAccountID, SessionID: testAdminSessionID, JWTID: testAdminJWTID,
		Role: auth.RoleAdmin, AuthRevision: 1,
	}
}

func testStudentPrincipal() auth.AccessPrincipal {
	return auth.AccessPrincipal{
		AccountID: testStudentID, SessionID: testStudentSession, JWTID: testStudentJWT,
		Role: auth.RoleStudent, AuthRevision: 1,
	}
}
