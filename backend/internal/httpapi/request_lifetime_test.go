package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/artifact"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/health"
	"github.com/kkkzbh/AscendAny/backend/internal/importing"
)

var errTestBodyClosed = errors.New("test request body closed")

type blockingRequestBody struct {
	closed chan struct{}
	once   sync.Once
}

func newBlockingRequestBody() *blockingRequestBody {
	return &blockingRequestBody{closed: make(chan struct{})}
}

func (body *blockingRequestBody) Read([]byte) (int, error) {
	<-body.closed
	return 0, errTestBodyClosed
}

func (body *blockingRequestBody) Close() error {
	body.once.Do(func() { close(body.closed) })
	return nil
}

func TestLoginBodyDeadlineClosesSlowBodyAndReturnsRequestTimeout(t *testing.T) {
	t.Parallel()

	var loginCalls atomic.Int32
	service := stubAuthService{login: func(context.Context, auth.LoginInput) (auth.AuthResult, error) {
		loginCalls.Add(1)
		return auth.AuthResult{}, errors.New("login must not run")
	}}
	options := testHandlerOptions(health.Report{Status: health.StatusReady})
	options.Auth = service
	options.AuthBodyTimeout = 50 * time.Millisecond
	handler, err := New(options)
	if err != nil {
		t.Fatal(err)
	}

	body := newBlockingRequestBody()
	request := authRequest(http.MethodPost, "/api/v2/auth/login", "")
	request.Body = body
	request.ContentLength = -1
	request.Header.Set("Content-Type", "application/json")
	response := newTestResponseRecorder()
	started := time.Now()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusRequestTimeout {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if loginCalls.Load() != 0 {
		t.Fatalf("login calls=%d", loginCalls.Load())
	}
	if time.Since(started) > time.Second {
		t.Fatal("slow authentication body was not interrupted promptly")
	}
	select {
	case <-body.closed:
	default:
		t.Fatal("slow authentication body was not closed")
	}
}

func TestUploadDeadlineClosesSlowBodyAndReturnsRequestTimeout(t *testing.T) {
	t.Parallel()

	service := stubAuthService{me: func(context.Context, string) (auth.Account, error) {
		return auth.Account{ID: testImportJobID, Role: auth.RoleAdmin}, nil
	}}
	publisher := stubArtifactPublisher{publish: func(ctx context.Context, reader io.Reader) (*artifact.Publication, error) {
		_, _ = io.ReadAll(reader)
		if err := ctx.Err(); err != nil {
			return nil, &artifact.StoreError{Code: artifact.ErrorCanceled, Op: "test upload", Err: err}
		}
		return nil, errors.New("upload read ended without a context deadline")
	}}
	options := testHandlerOptions(health.Report{Status: health.StatusReady})
	options.Auth = service
	options.Artifacts = publisher
	options.Imports = unusedImportQueue{}
	options.ImportReader = unusedImportReader{}
	options.UploadBodyTimeout = 50 * time.Millisecond
	handler, err := New(options)
	if err != nil {
		t.Fatal(err)
	}

	body := newBlockingRequestBody()
	request := importRequest(http.MethodPost, "/api/v2/imports/pintia", "")
	request.Body = body
	request.ContentLength = -1
	request.Header.Set("Content-Type", importing.PintiaSnapshotV2MediaType)
	response := newTestResponseRecorder()
	started := time.Now()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusRequestTimeout {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if time.Since(started) > time.Second {
		t.Fatal("slow upload body was not interrupted promptly")
	}
	select {
	case <-body.closed:
	default:
		t.Fatal("slow upload body was not closed")
	}
}
