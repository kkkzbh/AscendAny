package feedback

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"path/filepath"
	"sort"
	"testing"
	"time"

	artifactstore "github.com/kkkzbh/AscendAny/backend/internal/artifact"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

type applicationVerifier struct {
	principal auth.AccessPrincipal
}

func (verifier applicationVerifier) VerifyAccessToken(token string) (auth.AccessPrincipal, error) {
	if token != "access-token" {
		panic("unexpected access token")
	}
	return verifier.principal, nil
}

type applicationSubmissionService func(context.Context, SubmitInput) (SubmitResult, error)

func (service applicationSubmissionService) SubmitAuthenticated(ctx context.Context, input SubmitInput) (SubmitResult, error) {
	return service(ctx, input)
}

type recordingPublicationStore struct {
	inner  *artifactstore.Store
	hashes []string
}

func (store *recordingPublicationStore) Publish(ctx context.Context, source io.Reader) (*artifactstore.Publication, error) {
	content, err := io.ReadAll(source)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(content)
	store.hashes = append(store.hashes, hex.EncodeToString(digest[:]))
	return store.inner.Publish(ctx, bytes.NewReader(content))
}

func TestApplicationPublishesUniqueHashesInGlobalOrderAndKeepsSequence(t *testing.T) {
	store, err := artifactstore.NewStore(filepath.Join(t.TempDir(), "artifacts"), MaxImageBytes)
	if err != nil {
		t.Fatal(err)
	}
	recording := &recordingPublicationStore{inner: store}
	firstContent := []byte("first image")
	secondContent := []byte("second image")
	firstHash := imageHash(firstContent)
	secondHash := imageHash(secondContent)
	expectedPublishOrder := []string{firstHash, secondHash}
	sort.Strings(expectedPublishOrder)

	serviceCalled := false
	service := applicationSubmissionService(func(_ context.Context, input SubmitInput) (SubmitResult, error) {
		serviceCalled = true
		if len(input.Attachments) != 3 || input.Attachments[0].SHA256 != secondHash ||
			input.Attachments[1].SHA256 != firstHash || input.Attachments[2].SHA256 != secondHash ||
			input.Attachments[0].Sequence != 1 || input.Attachments[1].Sequence != 2 || input.Attachments[2].Sequence != 3 {
			t.Fatalf("attachments=%#v", input.Attachments)
		}
		blocked, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
		defer cancel()
		if publication, publishErr := store.Publish(blocked, bytes.NewReader(secondContent)); publishErr == nil {
			_ = publication.Release()
			t.Fatal("duplicate publication acquired while feedback transaction was active")
		}
		return SubmitResult{Created: true, Submission: Submission{
			ID: testFeedbackID, DeliveryJobID: testDeliveryID, CreatedAt: time.Now().UTC(),
		}}, nil
	})
	application, err := NewApplicationService(applicationVerifier{principal: validSubmitInput().Principal}, service, recording)
	if err != nil {
		t.Fatal(err)
	}
	input := validApplicationInput()
	input.Images = []ImageInput{
		{Name: "second.png", DataURL: imageDataURL("image/png", secondContent)},
		{Name: "first.png", DataURL: imageDataURL("image/png", firstContent)},
		{Name: "second-copy.png", DataURL: imageDataURL("image/png", secondContent)},
	}
	result, err := application.SubmitAuthenticated(context.Background(), "access-token", input)
	if err != nil || !result.Created || !serviceCalled {
		t.Fatalf("result=%#v serviceCalled=%t error=%v", result, serviceCalled, err)
	}
	if len(recording.hashes) != 2 || recording.hashes[0] != expectedPublishOrder[0] || recording.hashes[1] != expectedPublishOrder[1] {
		t.Fatalf("publication order=%v want=%v", recording.hashes, expectedPublishOrder)
	}
	publication, err := store.Publish(context.Background(), bytes.NewReader(secondContent))
	if err != nil {
		t.Fatalf("publication lock was not released: %v", err)
	}
	if err := publication.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestApplicationPreflightsDomainBeforePublishing(t *testing.T) {
	store, err := artifactstore.NewStore(filepath.Join(t.TempDir(), "artifacts"), MaxImageBytes)
	if err != nil {
		t.Fatal(err)
	}
	recording := &recordingPublicationStore{inner: store}
	service := applicationSubmissionService(func(context.Context, SubmitInput) (SubmitResult, error) {
		t.Fatal("invalid feedback reached submission service")
		return SubmitResult{}, nil
	})
	application, err := NewApplicationService(applicationVerifier{principal: validSubmitInput().Principal}, service, recording)
	if err != nil {
		t.Fatal(err)
	}
	input := validApplicationInput()
	input.Title = " invalid"
	input.Images = []ImageInput{{Name: "screen.png", DataURL: imageDataURL("image/png", []byte("image"))}}
	if _, err := application.SubmitAuthenticated(context.Background(), "access-token", input); CodeOf(err) != ErrorInvalidInput {
		t.Fatalf("error=%v code=%q", err, CodeOf(err))
	}
	if len(recording.hashes) != 0 {
		t.Fatalf("invalid request published artifacts: %v", recording.hashes)
	}
}

func TestReadOnlyApplicationFailsAtExplicitWriteBoundary(t *testing.T) {
	application, err := NewReadOnlyApplicationService(
		applicationVerifier{principal: validSubmitInput().Principal},
		applicationSubmissionService(func(context.Context, SubmitInput) (SubmitResult, error) {
			t.Fatal("read-only application reached submission service")
			return SubmitResult{}, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.SubmitAuthenticated(context.Background(), "access-token", validApplicationInput()); CodeOf(err) != ErrorWritesDisabled {
		t.Fatalf("error=%v code=%q", err, CodeOf(err))
	}
}

func validApplicationInput() ApplicationInput {
	input := validSubmitInput()
	return ApplicationInput{
		ClientRequestID: input.ClientRequestID, Title: input.Title, Content: input.Content,
		Platform: input.Platform, AppVersion: input.AppVersion, UserAgent: input.UserAgent,
	}
}

func imageDataURL(mediaType string, content []byte) string {
	return "data:" + mediaType + ";base64," + base64.StdEncoding.EncodeToString(content)
}

func imageHash(content []byte) string {
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:])
}
