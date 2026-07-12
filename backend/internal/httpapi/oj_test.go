package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/health"
	"github.com/kkkzbh/AscendAny/backend/internal/oj"
)

const (
	testOJProblemID    = "123e4567-e89b-42d3-a456-426614174040"
	testOJSubmissionID = "123e4567-e89b-42d3-a456-426614174041"
	testOJJudgeJobID   = "123e4567-e89b-42d3-a456-426614174042"
	testOJRequestID    = "123e4567-e89b-42d3-a456-426614174043"
)

type ojServiceStub struct {
	authorizeUpload      func(context.Context, string, oj.UploadKind) (oj.UploadAuthorization, error)
	listProblems         func(context.Context, string, *string, int, bool) (oj.ProblemPage, error)
	getProblem           func(context.Context, string, string) (oj.Problem, bool, error)
	createProblemVersion func(context.Context, oj.UploadAuthorization, oj.ProblemVersionMetadata, io.Reader) (oj.CreateProblemVersionResult, error)
	createSubmission     func(context.Context, oj.UploadAuthorization, oj.SubmissionMetadata, io.Reader, io.Reader) (oj.CreateSubmissionResult, error)
	getSubmission        func(context.Context, string, string) (oj.SubmissionDetail, bool, error)
	readJudgeEvents      func(context.Context, string, string, int64, int) (oj.JudgeEventBatch, bool, error)
}

func (stub ojServiceStub) AuthorizeUpload(ctx context.Context, token string, kind oj.UploadKind) (oj.UploadAuthorization, error) {
	if stub.authorizeUpload != nil {
		return stub.authorizeUpload(ctx, token, kind)
	}
	return oj.UploadAuthorization{}, nil
}

func (stub ojServiceStub) ListProblems(ctx context.Context, access string, cursor *string, limit int, includeArchived bool) (oj.ProblemPage, error) {
	if stub.listProblems == nil {
		panic("unexpected OJ problem list")
	}
	return stub.listProblems(ctx, access, cursor, limit, includeArchived)
}

func (stub ojServiceStub) GetProblem(ctx context.Context, access, problemID string) (oj.Problem, bool, error) {
	if stub.getProblem == nil {
		panic("unexpected OJ problem read")
	}
	return stub.getProblem(ctx, access, problemID)
}

func (stub ojServiceStub) CreateProblemVersion(
	ctx context.Context,
	authorization oj.UploadAuthorization,
	metadata oj.ProblemVersionMetadata,
	testBundle io.Reader,
) (oj.CreateProblemVersionResult, error) {
	if stub.createProblemVersion == nil {
		panic("unexpected OJ problem version creation")
	}
	return stub.createProblemVersion(ctx, authorization, metadata, testBundle)
}

func (stub ojServiceStub) CreateSubmission(
	ctx context.Context,
	authorization oj.UploadAuthorization,
	metadata oj.SubmissionMetadata,
	source io.Reader,
	stdin io.Reader,
) (oj.CreateSubmissionResult, error) {
	if stub.createSubmission == nil {
		panic("unexpected OJ submission creation")
	}
	return stub.createSubmission(ctx, authorization, metadata, source, stdin)
}

func (stub ojServiceStub) GetSubmission(ctx context.Context, access, submissionID string) (oj.SubmissionDetail, bool, error) {
	if stub.getSubmission == nil {
		panic("unexpected OJ submission read")
	}
	return stub.getSubmission(ctx, access, submissionID)
}

func (stub ojServiceStub) ReadJudgeEvents(
	ctx context.Context,
	access, submissionID string,
	after int64,
	limit int,
) (oj.JudgeEventBatch, bool, error) {
	if stub.readJudgeEvents == nil {
		panic("unexpected OJ judge event read")
	}
	return stub.readJudgeEvents(ctx, access, submissionID, after, limit)
}

func TestOJProblemReadsUseStrictOwnedContracts(t *testing.T) {
	t.Parallel()
	problem := testOJProblem()
	listCalls := 0
	getCalls := 0
	service := ojServiceStub{
		listProblems: func(_ context.Context, access string, cursor *string, limit int, includeArchived bool) (oj.ProblemPage, error) {
			listCalls++
			if access != "oj-token" || cursor == nil || *cursor != "two_sum" || limit != 7 || !includeArchived {
				t.Fatalf("list access=%q cursor=%v limit=%d archived=%t", access, cursor, limit, includeArchived)
			}
			return oj.ProblemPage{Items: []oj.Problem{problem}}, nil
		},
		getProblem: func(_ context.Context, access, problemID string) (oj.Problem, bool, error) {
			getCalls++
			if access != "oj-token" || problemID != testOJProblemID {
				t.Fatalf("get access=%q problem=%q", access, problemID)
			}
			return problem, true, nil
		},
	}
	handler := newOJTestHandler(t, service, true)

	listResponse := newTestResponseRecorder()
	handler.ServeHTTP(listResponse, ojRequest(http.MethodGet, "/api/v2/oj/problems?afterSlug=two_sum&limit=7&includeArchived=true", nil, ""))
	if listResponse.Code != http.StatusOK || !strings.Contains(listResponse.Body.String(), `"slug":"two_sum"`) {
		t.Fatalf("list status=%d body=%s", listResponse.Code, listResponse.Body.String())
	}

	getResponse := newTestResponseRecorder()
	handler.ServeHTTP(getResponse, ojRequest(http.MethodGet, "/api/v2/oj/problems/"+testOJProblemID, nil, ""))
	if getResponse.Code != http.StatusOK || !strings.Contains(getResponse.Body.String(), `"id":"`+testOJProblemID+`"`) {
		t.Fatalf("get status=%d body=%s", getResponse.Code, getResponse.Body.String())
	}

	for _, path := range []string{
		"/api/v2/oj/problems?",
		"/api/v2/oj/problems?afterSlug=TwoSum",
		"/api/v2/oj/problems?limit=01",
		"/api/v2/oj/problems?includeArchived=false",
		"/api/v2/oj/problems?unknown=1",
		"/api/v2/oj/problems/invalid",
		"/api/v2/oj/problems/" + testOJProblemID + "?x=1",
	} {
		response := newTestResponseRecorder()
		handler.ServeHTTP(response, ojRequest(http.MethodGet, path, nil, ""))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid path=%s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
	if listCalls != 1 || getCalls != 1 {
		t.Fatalf("invalid requests reached service: list=%d get=%d", listCalls, getCalls)
	}
}

func TestOJProblemMultipartPublishesOnlyAfterClosedBodyValidation(t *testing.T) {
	t.Parallel()
	metadata := testOJProblemMetadata()
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	bundle := []byte("deterministic-test-bundle")
	serviceCalls := 0
	finished := false
	service := ojServiceStub{createProblemVersion: func(
		_ context.Context,
		_ oj.UploadAuthorization,
		input oj.ProblemVersionMetadata,
		reader io.Reader,
	) (oj.CreateProblemVersionResult, error) {
		serviceCalls++
		if input.Slug != metadata.Slug || input.ExpectedHeadRevision != 0 {
			t.Fatalf("metadata=%#v", input)
		}
		stored, readErr := io.ReadAll(reader)
		if readErr != nil {
			return oj.CreateProblemVersionResult{}, readErr
		}
		if !bytes.Equal(stored, bundle) {
			t.Fatalf("bundle=%q", stored)
		}
		finished = true
		return oj.CreateProblemVersionResult{Problem: testOJProblem()}, nil
	}}
	handler := newOJTestHandler(t, service, true)
	body, contentType := makeOJMultipart(t,
		ojMultipartPart{name: "metadata", mediaType: "application/json", body: metadataJSON},
		ojMultipartPart{name: "testBundle", filename: "tests.tar", mediaType: oj.TestBundleMediaType, body: bundle},
	)
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, ojRequest(http.MethodPost, "/api/v2/admin/oj/problems/versions", body, contentType))
	if response.Code != http.StatusCreated || !finished || serviceCalls != 1 {
		t.Fatalf("status=%d finished=%t calls=%d body=%s", response.Code, finished, serviceCalls, response.Body.String())
	}
	if response.Header().Get("Location") != "/api/v2/oj/problems/"+testOJProblemID {
		t.Fatalf("Location=%q", response.Header().Get("Location"))
	}
}

func TestOJProblemMultipartRejectsTrailingPartBeforeCoreMutation(t *testing.T) {
	t.Parallel()
	metadataJSON, err := json.Marshal(testOJProblemMetadata())
	if err != nil {
		t.Fatal(err)
	}
	coreMutation := false
	service := ojServiceStub{createProblemVersion: func(
		_ context.Context,
		_ oj.UploadAuthorization,
		_ oj.ProblemVersionMetadata,
		reader io.Reader,
	) (oj.CreateProblemVersionResult, error) {
		_, readErr := io.ReadAll(reader)
		if readErr != nil {
			return oj.CreateProblemVersionResult{}, readErr
		}
		coreMutation = true
		return oj.CreateProblemVersionResult{}, nil
	}}
	handler := newOJTestHandler(t, service, true)
	body, contentType := makeOJMultipart(t,
		ojMultipartPart{name: "metadata", mediaType: "application/json", body: metadataJSON},
		ojMultipartPart{name: "testBundle", filename: "tests.tar", mediaType: oj.TestBundleMediaType, body: []byte("bundle")},
		ojMultipartPart{name: "unexpected", filename: "extra.bin", mediaType: "application/octet-stream", body: []byte("extra")},
	)
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, ojRequest(http.MethodPost, "/api/v2/admin/oj/problems/versions", body, contentType))
	if response.Code != http.StatusBadRequest || coreMutation || !strings.Contains(response.Body.String(), `"code":"invalid_multipart"`) {
		t.Fatalf("status=%d mutated=%t body=%s", response.Code, coreMutation, response.Body.String())
	}
}

func TestOJUploadsAuthorizeActivePrincipalBeforeReadingBody(t *testing.T) {
	t.Parallel()
	var reads int
	body := &observedOJRequestBody{onRead: func() { reads++ }}
	service := ojServiceStub{authorizeUpload: func(_ context.Context, token string, kind oj.UploadKind) (oj.UploadAuthorization, error) {
		if token != "oj-token" || kind != oj.UploadSubmission {
			t.Fatalf("token=%q kind=%q", token, kind)
		}
		return oj.UploadAuthorization{}, &auth.Error{
			Code: auth.ErrorAuthentication, Message: "Authentication was rejected.", Cause: errors.New("revoked session"),
		}
	}}
	options := testHandlerOptions(health.Report{Status: health.StatusReady})
	options.OJ = service
	options.UploadBodyTimeout = 50 * time.Millisecond
	handler, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	request := ojRequest(http.MethodPost, "/api/v2/oj/submissions", nil, "multipart/form-data; boundary=closed")
	request.Body = body
	request.ContentLength = -1
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || reads != 0 || !body.closed {
		t.Fatalf("status=%d reads=%d closed=%t body=%s", response.Code, reads, body.closed, response.Body.String())
	}
}

func TestOJMultipartRejectsEntityEpilogueBeforeMutation(t *testing.T) {
	t.Parallel()
	problemMetadata, err := json.Marshal(testOJProblemMetadata())
	if err != nil {
		t.Fatal(err)
	}
	problemMutated := false
	problemService := ojServiceStub{createProblemVersion: func(
		_ context.Context,
		_ oj.UploadAuthorization,
		_ oj.ProblemVersionMetadata,
		reader io.Reader,
	) (oj.CreateProblemVersionResult, error) {
		if _, err := io.ReadAll(reader); err != nil {
			return oj.CreateProblemVersionResult{}, err
		}
		problemMutated = true
		return oj.CreateProblemVersionResult{}, nil
	}}
	problemBody, problemType := makeOJMultipart(t,
		ojMultipartPart{name: "metadata", mediaType: "application/json", body: problemMetadata},
		ojMultipartPart{name: "testBundle", filename: "tests.tar", mediaType: oj.TestBundleMediaType, body: []byte("bundle")},
	)
	problemBody = append(problemBody, []byte("forbidden-epilogue")...)
	problemResponse := newTestResponseRecorder()
	newOJTestHandler(t, problemService, true).ServeHTTP(
		problemResponse,
		ojRequest(http.MethodPost, "/api/v2/admin/oj/problems/versions", problemBody, problemType),
	)
	if problemResponse.Code != http.StatusBadRequest || problemMutated {
		t.Fatalf("problem status=%d mutated=%t body=%s", problemResponse.Code, problemMutated, problemResponse.Body.String())
	}

	submissionMetadata, err := json.Marshal(testOJSubmissionMetadata(oj.SubmissionSubmit))
	if err != nil {
		t.Fatal(err)
	}
	submissionCalls := 0
	submissionService := ojServiceStub{createSubmission: func(context.Context, oj.UploadAuthorization, oj.SubmissionMetadata, io.Reader, io.Reader) (oj.CreateSubmissionResult, error) {
		submissionCalls++
		return oj.CreateSubmissionResult{}, nil
	}}
	submissionBody, submissionType := makeOJMultipart(t,
		ojMultipartPart{name: "metadata", mediaType: "application/json", body: submissionMetadata},
		ojMultipartPart{name: "source", filename: "main.cpp", mediaType: oj.CPP20SourceMediaType, body: []byte("int main() {}")},
	)
	submissionBody = append(submissionBody, []byte("forbidden-epilogue")...)
	submissionResponse := newTestResponseRecorder()
	newOJTestHandler(t, submissionService, true).ServeHTTP(
		submissionResponse,
		ojRequest(http.MethodPost, "/api/v2/oj/submissions", submissionBody, submissionType),
	)
	if submissionResponse.Code != http.StatusBadRequest || submissionCalls != 0 {
		t.Fatalf("submission status=%d calls=%d body=%s", submissionResponse.Code, submissionCalls, submissionResponse.Body.String())
	}
}

func TestOJProblemUploadWaitsForHTTPEntityEOFBeforeMutation(t *testing.T) {
	t.Parallel()
	metadata, err := json.Marshal(testOJProblemMetadata())
	if err != nil {
		t.Fatal(err)
	}
	prefix, contentType := makeOJMultipart(t,
		ojMultipartPart{name: "metadata", mediaType: "application/json", body: metadata},
		ojMultipartPart{name: "testBundle", filename: "tests.tar", mediaType: oj.TestBundleMediaType, body: []byte("bundle")},
	)
	body := newPrefixBlockingOJBody(prefix)
	mutated := false
	service := ojServiceStub{createProblemVersion: func(
		_ context.Context,
		_ oj.UploadAuthorization,
		_ oj.ProblemVersionMetadata,
		reader io.Reader,
	) (oj.CreateProblemVersionResult, error) {
		if _, err := io.ReadAll(reader); err != nil {
			return oj.CreateProblemVersionResult{}, err
		}
		mutated = true
		return oj.CreateProblemVersionResult{}, nil
	}}
	options := testHandlerOptions(health.Report{Status: health.StatusReady})
	options.OJ = service
	options.UploadBodyTimeout = 50 * time.Millisecond
	handler, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	request := ojRequest(http.MethodPost, "/api/v2/admin/oj/problems/versions", nil, contentType)
	request.Body = body
	request.ContentLength = -1
	response := newTestResponseRecorder()
	started := time.Now()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusRequestTimeout || mutated || time.Since(started) > time.Second {
		t.Fatalf("status=%d mutated=%t duration=%s body=%s", response.Code, mutated, time.Since(started), response.Body.String())
	}
	select {
	case <-body.closed:
	default:
		t.Fatal("incomplete OJ entity was not closed")
	}
}

func TestOJMultipartHeaderRejectionDoesNotDrainPartBody(t *testing.T) {
	t.Parallel()
	boundary := "closed-boundary"
	prefix := []byte("--" + boundary + "\r\n" +
		"Content-Disposition: form-data; name=\"wrong\"\r\n" +
		"Content-Type: application/json\r\n\r\n")
	body := newPrefixBlockingOJBody(prefix)
	options := testHandlerOptions(health.Report{Status: health.StatusReady})
	options.OJ = ojServiceStub{}
	options.UploadBodyTimeout = 5 * time.Second
	handler, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	request := ojRequest(http.MethodPost, "/api/v2/oj/submissions", nil, "multipart/form-data; boundary="+boundary)
	request.Body = body
	request.ContentLength = -1
	response := newTestResponseRecorder()
	started := time.Now()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || time.Since(started) > time.Second {
		t.Fatalf("status=%d duration=%s body=%s", response.Code, time.Since(started), response.Body.String())
	}
	select {
	case <-body.closed:
	default:
		t.Fatal("rejected multipart body was not closed")
	}
}

func TestOJMultipartRejectsAmbiguousContentDisposition(t *testing.T) {
	t.Parallel()
	metadata, err := json.Marshal(testOJSubmissionMetadata(oj.SubmissionSubmit))
	if err != nil {
		t.Fatal(err)
	}
	serviceCalls := 0
	service := ojServiceStub{createSubmission: func(context.Context, oj.UploadAuthorization, oj.SubmissionMetadata, io.Reader, io.Reader) (oj.CreateSubmissionResult, error) {
		serviceCalls++
		return oj.CreateSubmissionResult{}, nil
	}}
	handler := newOJTestHandler(t, service, true)
	for _, dispositions := range [][]string{
		{`form-data; name="metadata"`, `form-data; name="metadata"`},
		{`form-data; name="metadata"; size=1`},
	} {
		body, contentType := makeOJMultipartWithMetadataDisposition(t, metadata, dispositions)
		response := newTestResponseRecorder()
		handler.ServeHTTP(response, ojRequest(http.MethodPost, "/api/v2/oj/submissions", body, contentType))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("dispositions=%v status=%d body=%s", dispositions, response.Code, response.Body.String())
		}
	}
	if serviceCalls != 0 {
		t.Fatalf("ambiguous disposition reached service: calls=%d", serviceCalls)
	}
}

func TestOJUnknownLengthProblemUploadMapsTotalLimitTo413(t *testing.T) {
	t.Parallel()
	policy := oj.DefaultPolicy()
	policy.MaximumTestBundleBytes = 1
	metadata, err := json.Marshal(testOJProblemMetadata())
	if err != nil {
		t.Fatal(err)
	}
	body, contentType := makeOJMultipart(t,
		ojMultipartPart{name: "metadata", mediaType: "application/json", body: metadata},
		ojMultipartPart{
			name: "testBundle", filename: "tests.tar", mediaType: oj.TestBundleMediaType,
			body: bytes.Repeat([]byte("x"), int(maximumOJProblemRequestBytes(policy))),
		},
	)
	service := ojServiceStub{createProblemVersion: func(
		_ context.Context,
		_ oj.UploadAuthorization,
		_ oj.ProblemVersionMetadata,
		reader io.Reader,
	) (oj.CreateProblemVersionResult, error) {
		_, err := io.ReadAll(reader)
		return oj.CreateProblemVersionResult{}, err
	}}
	options := testHandlerOptions(health.Report{Status: health.StatusReady})
	options.OJ = service
	options.OJPolicy = policy
	handler, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	request := ojRequest(http.MethodPost, "/api/v2/admin/oj/problems/versions", body, contentType)
	request.ContentLength = -1
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge || !strings.Contains(response.Body.String(), `"code":"payload_too_large"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestOJSubmissionMultipartSupportsRunAndSubmit(t *testing.T) {
	t.Parallel()
	source := []byte("int main() { return 0; }\n")
	stdin := []byte("4 7\n")
	serviceCalls := 0
	service := ojServiceStub{createSubmission: func(
		_ context.Context,
		_ oj.UploadAuthorization,
		metadata oj.SubmissionMetadata,
		sourceReader io.Reader,
		stdinReader io.Reader,
	) (oj.CreateSubmissionResult, error) {
		serviceCalls++
		if metadata.ProblemID != testOJProblemID || metadata.LanguageID != oj.LanguageCPP20 {
			t.Fatalf("metadata=%#v", metadata)
		}
		receivedSource, err := io.ReadAll(sourceReader)
		if err != nil || !bytes.Equal(receivedSource, source) {
			t.Fatalf("source=%q err=%v", receivedSource, err)
		}
		if metadata.Mode == oj.SubmissionRun {
			receivedStdin, err := io.ReadAll(stdinReader)
			if err != nil || !bytes.Equal(receivedStdin, stdin) {
				t.Fatalf("stdin=%q err=%v", receivedStdin, err)
			}
		} else if stdinReader != nil {
			t.Fatal("submit mode received stdin")
		}
		return oj.CreateSubmissionResult{Submission: testOJSubmission(metadata.Mode), Created: true}, nil
	}}
	handler := newOJTestHandler(t, service, true)

	for _, mode := range []oj.SubmissionMode{oj.SubmissionRun, oj.SubmissionSubmit} {
		metadataJSON, err := json.Marshal(testOJSubmissionMetadata(mode))
		if err != nil {
			t.Fatal(err)
		}
		parts := []ojMultipartPart{
			{name: "metadata", mediaType: "application/json", body: metadataJSON},
			{name: "source", filename: "main.cpp", mediaType: oj.CPP20SourceMediaType, body: source},
		}
		if mode == oj.SubmissionRun {
			parts = append(parts, ojMultipartPart{name: "stdin", filename: "stdin.txt", mediaType: oj.PlainTextMediaType, body: stdin})
		}
		body, contentType := makeOJMultipart(t, parts...)
		response := newTestResponseRecorder()
		handler.ServeHTTP(response, ojRequest(http.MethodPost, "/api/v2/oj/submissions", body, contentType))
		if response.Code != http.StatusAccepted || response.Header().Get("Location") != "/api/v2/oj/submissions/"+testOJSubmissionID {
			t.Fatalf("mode=%s status=%d location=%q body=%s", mode, response.Code, response.Header().Get("Location"), response.Body.String())
		}
	}
	if serviceCalls != 2 {
		t.Fatalf("submission calls=%d", serviceCalls)
	}
}

func TestOJSubmissionMultipartRejectsInvalidClosedShapesBeforeService(t *testing.T) {
	t.Parallel()
	serviceCalls := 0
	service := ojServiceStub{createSubmission: func(context.Context, oj.UploadAuthorization, oj.SubmissionMetadata, io.Reader, io.Reader) (oj.CreateSubmissionResult, error) {
		serviceCalls++
		return oj.CreateSubmissionResult{}, nil
	}}
	handler := newOJTestHandler(t, service, true)
	source := ojMultipartPart{name: "source", filename: "main.cpp", mediaType: oj.CPP20SourceMediaType, body: []byte("int main() {}")}
	stdin := ojMultipartPart{name: "stdin", filename: "stdin.txt", mediaType: oj.PlainTextMediaType, body: []byte("1\n")}

	runJSON, err := json.Marshal(testOJSubmissionMetadata(oj.SubmissionRun))
	if err != nil {
		t.Fatal(err)
	}
	submitJSON, err := json.Marshal(testOJSubmissionMetadata(oj.SubmissionSubmit))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		parts []ojMultipartPart
	}{
		{name: "run missing stdin", parts: []ojMultipartPart{{name: "metadata", mediaType: "application/json", body: runJSON}, source}},
		{name: "submit with stdin", parts: []ojMultipartPart{{name: "metadata", mediaType: "application/json", body: submitJSON}, source, stdin}},
		{name: "unknown trailing part", parts: []ojMultipartPart{{name: "metadata", mediaType: "application/json", body: submitJSON}, source, {name: "extra", filename: "extra.bin", mediaType: "application/octet-stream", body: []byte("x")}}},
	}
	for _, test := range tests {
		body, contentType := makeOJMultipart(t, test.parts...)
		response := newTestResponseRecorder()
		handler.ServeHTTP(response, ojRequest(http.MethodPost, "/api/v2/oj/submissions", body, contentType))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("case=%s status=%d body=%s", test.name, response.Code, response.Body.String())
		}
	}
	if serviceCalls != 0 {
		t.Fatalf("invalid shapes reached service: calls=%d", serviceCalls)
	}
}

func TestOJSubmissionReadAndEventStreamAreOwnedAndResumable(t *testing.T) {
	t.Parallel()
	detail := oj.SubmissionDetail{Submission: testOJSubmission(oj.SubmissionSubmit), Status: oj.JobCompleted, AttemptCount: 1, UpdatedAt: testOJTime()}
	getCalls := 0
	eventCalls := 0
	service := ojServiceStub{
		getSubmission: func(_ context.Context, access, submissionID string) (oj.SubmissionDetail, bool, error) {
			getCalls++
			if access != "oj-token" || submissionID != testOJSubmissionID {
				t.Fatalf("get access=%q submission=%q", access, submissionID)
			}
			return detail, true, nil
		},
		readJudgeEvents: func(_ context.Context, access, submissionID string, after int64, limit int) (oj.JudgeEventBatch, bool, error) {
			eventCalls++
			if access != "oj-token" || submissionID != testOJSubmissionID || after != 2 || limit != ojEventBatchSize {
				t.Fatalf("events access=%q submission=%q after=%d limit=%d", access, submissionID, after, limit)
			}
			return oj.JudgeEventBatch{Terminal: true, LastSequence: 3, Events: []oj.JudgeEvent{{
				Sequence: 3, Type: "completed", Payload: json.RawMessage(`{"verdict":"accepted"}`), CreatedAt: testOJTime(),
			}}}, true, nil
		},
	}
	handler := newOJTestHandler(t, service, true)

	getResponse := newTestResponseRecorder()
	handler.ServeHTTP(getResponse, ojRequest(http.MethodGet, "/api/v2/oj/submissions/"+testOJSubmissionID, nil, ""))
	if getResponse.Code != http.StatusOK || !strings.Contains(getResponse.Body.String(), `"status":"completed"`) {
		t.Fatalf("get status=%d body=%s", getResponse.Code, getResponse.Body.String())
	}

	eventRequest := ojRequest(http.MethodGet, "/api/v2/oj/submissions/"+testOJSubmissionID+"/events", nil, "")
	eventRequest.Header.Set("Last-Event-ID", "2")
	eventResponse := newTestResponseRecorder()
	handler.ServeHTTP(eventResponse, eventRequest)
	if eventResponse.Code != http.StatusOK || eventResponse.Header().Get("Content-Type") != "text/event-stream; charset=utf-8" {
		t.Fatalf("events status=%d headers=%#v body=%s", eventResponse.Code, eventResponse.Header(), eventResponse.Body.String())
	}
	for _, fragment := range []string{"id: 3\n", "event: completed\n", `"sequence":3`, `"verdict":"accepted"`} {
		if !strings.Contains(eventResponse.Body.String(), fragment) {
			t.Fatalf("event stream lacks %q: %s", fragment, eventResponse.Body.String())
		}
	}
	if getCalls != 1 || eventCalls != 1 {
		t.Fatalf("calls get=%d events=%d", getCalls, eventCalls)
	}
}

func TestOJTerminalEventStreamDrainsEveryFullPage(t *testing.T) {
	t.Parallel()
	calls := 0
	service := ojServiceStub{readJudgeEvents: func(
		_ context.Context,
		_ string,
		_ string,
		after int64,
		limit int,
	) (oj.JudgeEventBatch, bool, error) {
		calls++
		if limit != ojEventBatchSize {
			t.Fatalf("limit=%d", limit)
		}
		if after == 0 {
			events := make([]oj.JudgeEvent, ojEventBatchSize)
			for index := range events {
				sequence := int64(index + 1)
				events[index] = oj.JudgeEvent{Sequence: sequence, Type: "retry", Payload: json.RawMessage(`{}`), CreatedAt: testOJTime()}
			}
			return oj.JudgeEventBatch{Events: events, LastSequence: ojEventBatchSize, Terminal: true}, true, nil
		}
		if after != ojEventBatchSize {
			t.Fatalf("after=%d", after)
		}
		return oj.JudgeEventBatch{Events: []oj.JudgeEvent{{
			Sequence: 101, Type: "completed", Payload: json.RawMessage(`{}`), CreatedAt: testOJTime(),
		}}, LastSequence: 101, Terminal: true}, true, nil
	}}
	handler := newOJTestHandler(t, service, true)
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, ojRequest(http.MethodGet, "/api/v2/oj/submissions/"+testOJSubmissionID+"/events", nil, ""))
	if response.Code != http.StatusOK || calls != 2 || !strings.Contains(response.Body.String(), "id: 101\n") {
		t.Fatalf("status=%d calls=%d body-tail=%s", response.Code, calls, response.Body.String()[max(0, response.Body.Len()-512):])
	}
}

func TestOJHTTPMapsOwnedErrorsWithoutLeakingCauses(t *testing.T) {
	t.Parallel()
	secret := "database-password-leak"
	service := ojServiceStub{listProblems: func(context.Context, string, *string, int, bool) (oj.ProblemPage, error) {
		return oj.ProblemPage{}, &oj.Error{Code: oj.ErrorDatabase, Operation: "read problems", Cause: errors.New(secret)}
	}}
	handler := newOJTestHandler(t, service, true)
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, ojRequest(http.MethodGet, "/api/v2/oj/problems", nil, ""))
	if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), secret) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestOJCORSPreflightUsesExactRouteContracts(t *testing.T) {
	t.Parallel()
	handler := newOJTestHandler(t, unusedOJService{}, true)
	for _, test := range []struct {
		method  string
		path    string
		headers string
	}{
		{http.MethodGet, "/api/v2/oj/problems", "Authorization"},
		{http.MethodGet, "/api/v2/oj/problems/" + testOJProblemID, "Authorization"},
		{http.MethodPost, "/api/v2/admin/oj/problems/versions", "Authorization, Content-Type"},
		{http.MethodPost, "/api/v2/oj/submissions", "Authorization, Content-Type"},
		{http.MethodGet, "/api/v2/oj/submissions/" + testOJSubmissionID, "Authorization"},
		{http.MethodGet, "/api/v2/oj/submissions/" + testOJSubmissionID + "/events", "Authorization, Last-Event-ID"},
	} {
		request := httptest.NewRequest(http.MethodOptions, test.path, nil)
		request.RemoteAddr = "192.0.2.1:44000"
		request.Header.Set("Origin", testWebOrigin)
		request.Header.Set("Access-Control-Request-Method", test.method)
		request.Header.Set("Access-Control-Request-Headers", test.headers)
		response := newTestResponseRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNoContent || response.Header().Get("Access-Control-Allow-Methods") != test.method {
			t.Fatalf("path=%s status=%d headers=%#v body=%s", test.path, response.Code, response.Header(), response.Body.String())
		}
	}
}

type ojMultipartPart struct {
	name      string
	filename  string
	mediaType string
	body      []byte
}

type observedOJRequestBody struct {
	onRead func()
	closed bool
}

func (body *observedOJRequestBody) Read([]byte) (int, error) {
	body.onRead()
	return 0, errors.New("unexpected body read")
}

func (body *observedOJRequestBody) Close() error {
	body.closed = true
	return nil
}

type prefixBlockingOJBody struct {
	prefix  *bytes.Reader
	blocked *blockingRequestBody
	closed  <-chan struct{}
}

func newPrefixBlockingOJBody(prefix []byte) *prefixBlockingOJBody {
	blocked := newBlockingRequestBody()
	return &prefixBlockingOJBody{prefix: bytes.NewReader(prefix), blocked: blocked, closed: blocked.closed}
}

func (body *prefixBlockingOJBody) Read(buffer []byte) (int, error) {
	if body.prefix.Len() > 0 {
		return body.prefix.Read(buffer)
	}
	return body.blocked.Read(buffer)
}

func (body *prefixBlockingOJBody) Close() error {
	return body.blocked.Close()
}

func makeOJMultipart(t *testing.T, parts ...ojMultipartPart) ([]byte, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, item := range parts {
		header := make(textproto.MIMEHeader)
		disposition := `form-data; name="` + item.name + `"`
		if item.filename != "" {
			disposition += `; filename="` + item.filename + `"`
		}
		header.Set("Content-Disposition", disposition)
		header.Set("Content-Type", item.mediaType)
		part, err := writer.CreatePart(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(item.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return body.Bytes(), writer.FormDataContentType()
}

func makeOJMultipartWithMetadataDisposition(t *testing.T, metadata []byte, dispositions []string) ([]byte, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := make(textproto.MIMEHeader)
	for _, disposition := range dispositions {
		header.Add("Content-Disposition", disposition)
	}
	header.Set("Content-Type", "application/json")
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(metadata); err != nil {
		t.Fatal(err)
	}
	sourceHeader := make(textproto.MIMEHeader)
	sourceHeader.Set("Content-Disposition", `form-data; name="source"; filename="main.cpp"`)
	sourceHeader.Set("Content-Type", oj.CPP20SourceMediaType)
	source, err := writer.CreatePart(sourceHeader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Write([]byte("int main() {}")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return body.Bytes(), writer.FormDataContentType()
}

func newOJTestHandler(t *testing.T, service OJService, writes bool) http.Handler {
	t.Helper()
	options := testHandlerOptions(health.Report{Status: health.StatusReady})
	options.OJ = service
	options.Capabilities = testCapabilities(writes)
	if !writes {
		options.Artifacts = nil
		options.Imports = nil
		options.RecommendationQueue = nil
	}
	handler, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func ojRequest(method, path string, body []byte, contentType string) *http.Request {
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	request.RemoteAddr = "192.0.2.1:44000"
	request.Header.Set("Authorization", "Bearer oj-token")
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	return request
}

func testOJProblemMetadata() oj.ProblemVersionMetadata {
	return oj.ProblemVersionMetadata{
		Slug:                 "two_sum",
		ExpectedHeadRevision: 0,
		Lifecycle:            oj.LifecycleActive,
		Title:                "Two Sum",
		StatementMarkdown:    "Find two values.",
		SolutionMarkdown:     nil,
		KnowledgeTags:        []string{"arrays"},
		TimeLimitMS:          1_000,
		MemoryLimitBytes:     64 << 20,
		OutputLimitBytes:     1 << 20,
		ProblemSpec:          json.RawMessage(`{"checker":"exact"}`),
	}
}

func testOJSubmissionMetadata(mode oj.SubmissionMode) oj.SubmissionMetadata {
	return oj.SubmissionMetadata{
		ClientRequestID:             testOJRequestID,
		ProblemID:                   testOJProblemID,
		ExpectedProblemHeadRevision: 1,
		Mode:                        mode,
		LanguageID:                  oj.LanguageCPP20,
	}
}

func testOJProblem() oj.Problem {
	now := testOJTime()
	return oj.Problem{
		ID: testOJProblemID, Slug: "two_sum", HeadRevision: 1,
		CurrentVersion: &oj.ProblemVersion{
			Number: 1, Lifecycle: oj.LifecycleActive, Title: "Two Sum", StatementMarkdown: "Find two values.",
			KnowledgeTags: []string{"arrays"}, TimeLimitMS: 1_000, MemoryLimitBytes: 64 << 20,
			OutputLimitBytes: 1 << 20, ContentSHA256: strings.Repeat("a", 64), CreatedAt: now,
		},
		CreatedAt: now, UpdatedAt: now,
	}
}

func testOJSubmission(mode oj.SubmissionMode) oj.Submission {
	return oj.Submission{
		ID: testOJSubmissionID, JudgeJobID: testOJJudgeJobID, ProblemID: testOJProblemID,
		ProblemVersion: 1, Mode: mode, LanguageID: oj.LanguageCPP20, CreatedAt: testOJTime(),
	}
}

func testOJTime() time.Time {
	return time.Date(2026, 7, 11, 9, 30, 0, 0, time.UTC)
}
