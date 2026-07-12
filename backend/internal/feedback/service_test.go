package feedback

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

const (
	testAccountID  = "123e4567-e89b-42d3-a456-426614174000"
	testSessionID  = "123e4567-e89b-42d3-a456-426614174001"
	testJWTID      = "123e4567-e89b-42d3-a456-426614174002"
	testRequestID  = "123e4567-e89b-42d3-a456-426614174003"
	testFeedbackID = "123e4567-e89b-42d3-a456-426614174004"
	testDeliveryID = "123e4567-e89b-42d3-a456-426614174005"
)

type serviceRepository struct {
	command SubmitCommand
	result  SubmitResult
	err     error
}

func (repository *serviceRepository) SubmitAuthenticated(_ context.Context, command SubmitCommand) (SubmitResult, error) {
	repository.command = command
	return repository.result, repository.err
}

func TestSubmitAuthenticatedBuildsStableSubjectAndOwnedIDs(t *testing.T) {
	t.Parallel()
	createdAt := time.Date(2026, 7, 11, 1, 2, 3, 0, time.UTC)
	repository := &serviceRepository{result: SubmitResult{
		Created:    true,
		Submission: Submission{ID: testFeedbackID, DeliveryJobID: testDeliveryID, CreatedAt: createdAt},
	}}
	identifiers := []string{testFeedbackID, testDeliveryID}
	service, err := newService(repository, testPolicy(), func() (string, error) {
		value := identifiers[0]
		identifiers = identifiers[1:]
		return value, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	input := validSubmitInput()
	result, err := service.SubmitAuthenticated(context.Background(), input)
	if err != nil || !result.Created || result.Submission.ID != testFeedbackID {
		t.Fatalf("result=%#v error=%v", result, err)
	}
	if repository.command.FeedbackPublicID != testFeedbackID || repository.command.DeliveryPublicID != testDeliveryID ||
		repository.command.ClientRequestID != testRequestID || repository.command.subjectHex() == strings.Repeat("0", 64) {
		t.Fatalf("command=%#v", repository.command)
	}
}

func TestSubmitAuthenticatedRejectsMalformedInputBeforeRepository(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*SubmitInput)
	}{
		{name: "role", mutate: func(input *SubmitInput) { input.Principal.Role = "operator" }},
		{name: "request", mutate: func(input *SubmitInput) { input.ClientRequestID = "bad" }},
		{name: "title trim", mutate: func(input *SubmitInput) { input.Title = " title" }},
		{name: "content limit", mutate: func(input *SubmitInput) { input.Content = strings.Repeat("x", MaxContentBytes+1) }},
		{name: "metadata trim", mutate: func(input *SubmitInput) { value := " web"; input.Platform = &value }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository := &serviceRepository{}
			service, err := newService(repository, testPolicy(), func() (string, error) { return testFeedbackID, nil })
			if err != nil {
				t.Fatal(err)
			}
			input := validSubmitInput()
			test.mutate(&input)
			if _, err := service.SubmitAuthenticated(context.Background(), input); CodeOf(err) != ErrorInvalidInput {
				t.Fatalf("error=%v code=%q", err, CodeOf(err))
			}
			if repository.command.ClientRequestID != "" {
				t.Fatal("repository was called for invalid input")
			}
		})
	}
}

func TestServiceRejectsInvalidPolicyAndUUIDFailure(t *testing.T) {
	t.Parallel()
	repository := &serviceRepository{}
	if _, err := NewService(repository, Policy{}); CodeOf(err) != ErrorInvalidConfiguration {
		t.Fatalf("invalid policy error=%v", err)
	}
	service, err := newService(repository, testPolicy(), func() (string, error) { return "", errors.New("entropy unavailable") })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SubmitAuthenticated(context.Background(), validSubmitInput()); CodeOf(err) != ErrorInvalidConfiguration {
		t.Fatalf("UUID error=%v", err)
	}
}

func validSubmitInput() SubmitInput {
	platform := "desktop"
	version := "2.0.0"
	return SubmitInput{
		Principal: auth.AccessPrincipal{
			AccountID:    testAccountID,
			SessionID:    testSessionID,
			JWTID:        testJWTID,
			Role:         auth.RoleStudent,
			AuthRevision: 1,
		},
		ClientRequestID: testRequestID,
		Title:           "Import feedback",
		Content:         "The imported exam is visible.",
		Platform:        &platform,
		AppVersion:      &version,
	}
}

func testPolicy() Policy {
	return Policy{
		Window:                   time.Hour,
		MaximumSubmissions:       5,
		DeliveryConfigurationKey: "feedback.delivery.default",
	}
}
