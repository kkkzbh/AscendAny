package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/feedback"
	"github.com/kkkzbh/AscendAny/backend/internal/health"
)

type feedbackServiceStub struct {
	submit func(context.Context, string, feedback.ApplicationInput) (feedback.SubmitResult, error)
}

func (stub feedbackServiceStub) SubmitAuthenticated(ctx context.Context, access string, input feedback.ApplicationInput) (feedback.SubmitResult, error) {
	return stub.submit(ctx, access, input)
}

func TestFeedbackSubmissionUsesAuthenticatedAcceptedContract(t *testing.T) {
	t.Parallel()
	calls := 0
	now := time.Date(2026, 7, 11, 9, 30, 0, 0, time.UTC)
	service := feedbackServiceStub{submit: func(_ context.Context, access string, input feedback.ApplicationInput) (feedback.SubmitResult, error) {
		calls++
		if access != "student-token" || input.ClientRequestID != "22222222-2222-4222-8222-222222222222" || input.Title != "UI issue" || input.Content != "The page is blank." || input.Platform == nil || *input.Platform != "desktop" || input.AppVersion != nil || input.UserAgent != nil {
			t.Fatalf("access=%q input=%#v", access, input)
		}
		return feedback.SubmitResult{Submission: feedback.Submission{
			ID: "33333333-3333-4333-8333-333333333333", DeliveryJobID: "44444444-4444-4444-8444-444444444444", CreatedAt: now,
		}, Created: calls == 1}, nil
	}}
	handler := newFeedbackTestHandler(t, service, true)
	body := `{"clientRequestId":"22222222-2222-4222-8222-222222222222","title":"UI issue","content":"The page is blank.","platform":"desktop","appVersion":null}`
	for range 2 {
		request := feedbackRequest(body)
		response := newTestResponseRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusAccepted || !strings.Contains(response.Body.String(), `"deliveryJobId":"44444444-4444-4444-8444-444444444444"`) {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	}
	if calls != 2 {
		t.Fatalf("calls=%d", calls)
	}
}

func TestFeedbackBoundaryRejectsInvalidRequestsBeforeService(t *testing.T) {
	t.Parallel()
	calls := 0
	service := feedbackServiceStub{submit: func(context.Context, string, feedback.ApplicationInput) (feedback.SubmitResult, error) {
		calls++
		return feedback.SubmitResult{}, nil
	}}
	handler := newFeedbackTestHandler(t, service, true)
	valid := `{"clientRequestId":"22222222-2222-4222-8222-222222222222","title":"UI issue","content":"blank"}`
	for _, test := range []struct {
		path        string
		body        string
		authorize   bool
		contentType string
		wantStatus  int
	}{
		{path: "/api/v2/feedback?x=1", body: valid, authorize: true, contentType: "application/json", wantStatus: http.StatusBadRequest},
		{path: "/api/v2/feedback", body: valid, authorize: false, contentType: "application/json", wantStatus: http.StatusUnauthorized},
		{path: "/api/v2/feedback", body: `{}`, authorize: true, contentType: "application/json", wantStatus: http.StatusBadRequest},
		{path: "/api/v2/feedback", body: strings.Replace(valid, `}`, `,"extra":1}`, 1), authorize: true, contentType: "application/json", wantStatus: http.StatusBadRequest},
		{path: "/api/v2/feedback", body: valid, authorize: true, contentType: "text/plain", wantStatus: http.StatusUnsupportedMediaType},
	} {
		request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
		request.RemoteAddr = "192.0.2.1:44000"
		if test.authorize {
			request.Header.Set("Authorization", "Bearer student-token")
		}
		request.Header.Set("Content-Type", test.contentType)
		response := newTestResponseRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.wantStatus {
			t.Fatalf("path=%s status=%d want=%d body=%s", test.path, response.Code, test.wantStatus, response.Body.String())
		}
	}
	if calls != 0 {
		t.Fatalf("invalid requests reached service: %d", calls)
	}

	disabled := newFeedbackTestHandler(t, service, false)
	response := newTestResponseRecorder()
	disabled.ServeHTTP(response, feedbackRequest(valid))
	if response.Code != http.StatusServiceUnavailable || calls != 0 {
		t.Fatalf("disabled status=%d calls=%d body=%s", response.Code, calls, response.Body.String())
	}
}

func TestFeedbackErrorsUseStableStatusAndOpaqueDetails(t *testing.T) {
	t.Parallel()
	secret := "provider-secret"
	for _, test := range []struct {
		code       feedback.ErrorCode
		wantStatus int
	}{
		{feedback.ErrorInvalidInput, http.StatusBadRequest},
		{feedback.ErrorPrincipalRejected, http.StatusForbidden},
		{feedback.ErrorRateLimited, http.StatusTooManyRequests},
		{feedback.ErrorIdempotencyConflict, http.StatusConflict},
		{feedback.ErrorDeliveryUnavailable, http.StatusServiceUnavailable},
		{feedback.ErrorDatabase, http.StatusInternalServerError},
	} {
		service := feedbackServiceStub{submit: func(context.Context, string, feedback.ApplicationInput) (feedback.SubmitResult, error) {
			return feedback.SubmitResult{}, &feedback.Error{Code: test.code, Op: "submit", Cause: errors.New(secret)}
		}}
		handler := newFeedbackTestHandler(t, service, true)
		response := newTestResponseRecorder()
		handler.ServeHTTP(response, feedbackRequest(`{"clientRequestId":"22222222-2222-4222-8222-222222222222","title":"UI issue","content":"blank"}`))
		if response.Code != test.wantStatus || strings.Contains(response.Body.String(), secret) {
			t.Fatalf("code=%s status=%d body=%s", test.code, response.Code, response.Body.String())
		}
		if test.code == feedback.ErrorRateLimited && response.Header().Get("Retry-After") != "1" {
			t.Fatalf("rate limit retry header=%q", response.Header().Get("Retry-After"))
		}
	}
}

func newFeedbackTestHandler(t *testing.T, service FeedbackService, writes bool) http.Handler {
	t.Helper()
	options := testHandlerOptions(health.Report{Status: health.StatusReady})
	options.Feedback = service
	options.Capabilities = testCapabilities(writes)
	if !writes {
		options.Artifacts = nil
		options.Imports = nil
		options.ModelProbe = nil
	}
	handler, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func feedbackRequest(body string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/api/v2/feedback", strings.NewReader(body))
	request.RemoteAddr = "192.0.2.1:44000"
	request.Header.Set("Authorization", "Bearer student-token")
	request.Header.Set("Content-Type", "application/json")
	return request
}
