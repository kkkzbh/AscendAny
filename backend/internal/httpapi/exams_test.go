package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/examcatalog"
	"github.com/kkkzbh/AscendAny/backend/internal/health"
)

const (
	testExamID     = "44444444-4444-4444-8444-444444444444"
	testSnapshotID = "55555555-5555-4555-8555-555555555555"
)

type examCatalogStub struct {
	list      func(context.Context, string, *string, int) (examcatalog.Page, error)
	get       func(context.Context, string, string) (examcatalog.Detail, bool, error)
	listCalls int
	getCalls  int
	access    string
	cursor    *string
	limit     int
	requested string
}

func (stub *examCatalogStub) List(ctx context.Context, access string, cursor *string, limit int) (examcatalog.Page, error) {
	stub.listCalls++
	stub.access = access
	stub.cursor = cursor
	stub.limit = limit
	return stub.list(ctx, access, cursor, limit)
}

func (stub *examCatalogStub) Get(ctx context.Context, access string, examID string) (examcatalog.Detail, bool, error) {
	stub.getCalls++
	stub.access = access
	stub.requested = examID
	return stub.get(ctx, access, examID)
}

func TestExamListUsesStrictPaginationAndAuthenticatedCatalog(t *testing.T) {
	t.Parallel()
	summary := httpTestExamSummary()
	service := &examCatalogStub{
		list: func(context.Context, string, *string, int) (examcatalog.Page, error) {
			return examcatalog.Page{Items: []examcatalog.ExamSummary{summary}}, nil
		},
		get: func(context.Context, string, string) (examcatalog.Detail, bool, error) {
			panic("unexpected detail call")
		},
	}
	handler := newExamTestHandler(t, service)
	request := examRequest(http.MethodGet, "/api/v2/exams?limit=25&cursor="+testExamID)
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if service.listCalls != 1 || service.access != "exam-access" || service.limit != 25 || service.cursor == nil || *service.cursor != testExamID {
		t.Fatalf("service=%#v", service)
	}
	var page examcatalog.Page
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil || len(page.Items) != 1 || page.Items[0].ID != testExamID {
		t.Fatalf("page=%#v error=%v", page, err)
	}

	for _, path := range []string{
		"/api/v2/exams?",
		"/api/v2/exams?limit=01",
		"/api/v2/exams?limit=101",
		"/api/v2/exams?limit=1&limit=2",
		"/api/v2/exams?cursor=invalid",
		"/api/v2/exams?other=1",
	} {
		invalid := examRequest(http.MethodGet, path)
		invalidResponse := newTestResponseRecorder()
		handler.ServeHTTP(invalidResponse, invalid)
		if invalidResponse.Code != http.StatusBadRequest || !strings.Contains(invalidResponse.Body.String(), `"code":"invalid_exam_page"`) {
			t.Fatalf("path=%q status=%d body=%s", path, invalidResponse.Code, invalidResponse.Body.String())
		}
	}
	if service.listCalls != 1 {
		t.Fatalf("invalid pagination reached service: calls=%d", service.listCalls)
	}
}

func TestExamDetailMapsFoundNotFoundAndInvalidRequests(t *testing.T) {
	t.Parallel()
	summary := httpTestExamSummary()
	service := &examCatalogStub{
		list: func(context.Context, string, *string, int) (examcatalog.Page, error) {
			panic("unexpected list call")
		},
		get: func(_ context.Context, _ string, examID string) (examcatalog.Detail, bool, error) {
			if examID == testExamID {
				return examcatalog.Detail{ExamSummary: summary, Problems: []examcatalog.Problem{}}, true, nil
			}
			return examcatalog.Detail{}, false, nil
		},
	}
	handler := newExamTestHandler(t, service)
	request := examRequest(http.MethodGet, "/api/v2/exams/"+testExamID)
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || service.requested != testExamID || !strings.Contains(response.Body.String(), `"problems":[]`) {
		t.Fatalf("status=%d service=%#v body=%s", response.Code, service, response.Body.String())
	}

	unknownID := "66666666-6666-4666-8666-666666666666"
	unknown := examRequest(http.MethodGet, "/api/v2/exams/"+unknownID)
	unknownResponse := newTestResponseRecorder()
	handler.ServeHTTP(unknownResponse, unknown)
	if unknownResponse.Code != http.StatusNotFound || !strings.Contains(unknownResponse.Body.String(), `"code":"exam_not_found"`) {
		t.Fatalf("unknown status=%d body=%s", unknownResponse.Code, unknownResponse.Body.String())
	}

	for _, path := range []string{"/api/v2/exams/invalid", "/api/v2/exams/" + testExamID + "?x=1"} {
		invalid := examRequest(http.MethodGet, path)
		invalidResponse := newTestResponseRecorder()
		handler.ServeHTTP(invalidResponse, invalid)
		if invalidResponse.Code != http.StatusBadRequest {
			t.Fatalf("path=%q status=%d body=%s", path, invalidResponse.Code, invalidResponse.Body.String())
		}
	}
	if service.getCalls != 2 {
		t.Fatalf("invalid detail reached service: calls=%d", service.getCalls)
	}
}

func TestExamErrorsArePubliclyBounded(t *testing.T) {
	t.Parallel()
	secret := "database-password-leak"
	service := &examCatalogStub{
		list: func(context.Context, string, *string, int) (examcatalog.Page, error) {
			return examcatalog.Page{}, &examcatalog.Error{Code: examcatalog.ErrorDatabase, Op: "read", Cause: errors.New(secret)}
		},
		get: func(context.Context, string, string) (examcatalog.Detail, bool, error) {
			panic("unexpected detail call")
		},
	}
	handler := newExamTestHandler(t, service)
	request := examRequest(http.MethodGet, "/api/v2/exams")
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), secret) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func newExamTestHandler(t *testing.T, service ExamCatalogService) http.Handler {
	t.Helper()
	options := testHandlerOptions(health.Report{Status: health.StatusReady})
	options.ExamCatalog = service
	handler, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func examRequest(method, path string) *http.Request {
	request := httptest.NewRequest(method, path, nil)
	request.RemoteAddr = "192.0.2.1:44000"
	request.Header.Set("Authorization", "Bearer exam-access")
	return request
}

func httpTestExamSummary() examcatalog.ExamSummary {
	eventTime := time.Date(2026, 7, 10, 3, 4, 5, 0, time.UTC)
	totalScore := "300.0"
	return examcatalog.ExamSummary{
		ID:               testExamID,
		SnapshotID:       testSnapshotID,
		Platform:         "pintia",
		ProblemSetID:     "2039341868571590656",
		Title:            "集训",
		SourceURL:        "https://pintia.cn/problem-sets/2039341868571590656",
		TotalScore:       &totalScore,
		ProblemCount:     15,
		ParticipantCount: 35,
		RankingCount:     35,
		SubmissionCount:  624,
		SnapshotSequence: 1,
		HeadRevision:     1,
		ExporterVersion:  "2.0.5",
		ExportedAt:       eventTime,
		UpdatedAt:        eventTime,
	}
}
