package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/oj"
)

const (
	maxOJSubmissionMetadataBytes int64 = 8 << 10
	ojMultipartEnvelopeBytes     int64 = 128 << 10
	ojEventPollInterval                = 500 * time.Millisecond
	ojEventHeartbeat                   = 15 * time.Second
	ojEventBatchSize                   = 100
)

var errUnexpectedOJMultipartPart = errors.New("OJ multipart request contains an unexpected part")

func maximumOJProblemMetadataBytes(policy oj.Policy) int64 {
	return int64(policy.MaximumTitleBytes+policy.MaximumStatementBytes+policy.MaximumSolutionBytes+policy.MaximumProblemSpecBytes) + ojMultipartEnvelopeBytes
}

func maximumOJProblemRequestBytes(policy oj.Policy) int64 {
	return maximumOJProblemMetadataBytes(policy) + policy.MaximumTestBundleBytes + ojMultipartEnvelopeBytes
}

func maximumOJSubmissionRequestBytes(policy oj.Policy) int64 {
	return maxOJSubmissionMetadataBytes + policy.MaximumSourceBytes + policy.MaximumStdinBytes + ojMultipartEnvelopeBytes
}

func (handler *Handler) listOJProblems(writer http.ResponseWriter, request *http.Request) {
	if !requestBodyIsEmpty(request) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "request_body_not_allowed", "Request body must be empty.")
		return
	}
	afterSlug, limit, includeArchived, err := parseOJProblemPageQuery(request.URL.RawQuery, request.URL.ForceQuery)
	if err != nil {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_oj_problem_page", "OJ problem pagination is invalid.")
		return
	}
	access, ok := bearerToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	}
	page, err := handler.oj.ListProblems(request.Context(), access, afterSlug, limit, includeArchived)
	if err != nil {
		handler.handleOJError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, page)
}

func parseOJProblemPageQuery(rawQuery string, forceQuery bool) (*string, int, bool, error) {
	limit := oj.DefaultPageSize
	if rawQuery == "" && !forceQuery {
		return nil, limit, false, nil
	}
	fields, err := parseCanonicalQueryFields(rawQuery, forceQuery, map[string]struct{}{
		"afterSlug": {}, "limit": {}, "includeArchived": {},
	})
	if err != nil {
		return nil, 0, false, err
	}
	var afterSlug *string
	if value, present := fields["afterSlug"]; present {
		if !oj.ValidSlug(value) {
			return nil, 0, false, errors.New("OJ problem cursor is invalid")
		}
		afterSlug = &value
	}
	if value, present := fields["limit"]; present {
		limit, err = parseCanonicalPositiveDecimal(value, 1, oj.MaxPageSize)
		if err != nil {
			return nil, 0, false, err
		}
	}
	includeArchived := false
	if value, present := fields["includeArchived"]; present {
		if value != "true" {
			return nil, 0, false, errors.New("includeArchived must be true when present")
		}
		includeArchived = true
	}
	return afterSlug, limit, includeArchived, nil
}

func (handler *Handler) getOJProblem(writer http.ResponseWriter, request *http.Request) {
	if !requestBodyIsEmpty(request) || !handler.requireNoQuery(writer, request) {
		if !requestBodyIsEmpty(request) {
			handler.writeAPIError(writer, request, http.StatusBadRequest, "request_body_not_allowed", "Request body must be empty.")
		}
		return
	}
	problemID := request.PathValue("problemId")
	if !oj.ValidPublicID(problemID) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_oj_problem_id", "OJ problem ID is invalid.")
		return
	}
	access, ok := bearerToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	}
	problem, found, err := handler.oj.GetProblem(request.Context(), access, problemID)
	if err != nil {
		handler.handleOJError(writer, request, err)
		return
	}
	if !found {
		handler.writeAPIError(writer, request, http.StatusNotFound, "oj_problem_not_found", "OJ problem does not exist.")
		return
	}
	writeJSON(writer, http.StatusOK, problem)
}

func (handler *Handler) createOJProblemVersion(writer http.ResponseWriter, request *http.Request) {
	if !handler.requireNoQuery(writer, request) {
		return
	}
	access, ok := bearerToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	}
	authorization, err := handler.oj.AuthorizeUpload(request.Context(), access, oj.UploadProblemVersion)
	if err != nil {
		handler.handleOJError(writer, request, err)
		return
	}
	reader, err := beginOJMultipart(writer, request, maximumOJProblemRequestBytes(handler.ojPolicy))
	if err != nil {
		handler.handleOJMultipartError(writer, request, err)
		return
	}
	metadataPart, err := nextOJPart(reader, "metadata", "application/json", false)
	if err != nil {
		handler.handleOJMultipartError(writer, request, err)
		return
	}
	var metadata oj.ProblemVersionMetadata
	if err := decodeOJMetadataPart(metadataPart, maximumOJProblemMetadataBytes(handler.ojPolicy), &metadata); err != nil {
		handler.handleOJMultipartError(writer, request, err)
		return
	}
	testBundle, err := nextOJPart(reader, "testBundle", oj.TestBundleMediaType, true)
	if err != nil {
		handler.handleOJMultipartError(writer, request, err)
		return
	}
	finalReader := &finalOJMultipartPartReader{
		part: testBundle, reader: reader, readContext: requestBodyReadContext(request), request: request,
	}
	result, err := handler.oj.CreateProblemVersion(
		request.Context(), authorization, metadata, finalReader,
	)
	if err != nil {
		handler.handleOJError(writer, request, err)
		return
	}
	writer.Header().Set("Location", "/api/v2/oj/problems/"+result.Problem.ID)
	status := http.StatusCreated
	if result.Idempotent {
		status = http.StatusOK
	}
	writeJSON(writer, status, result)
}

func (handler *Handler) createOJSubmission(writer http.ResponseWriter, request *http.Request) {
	if !handler.requireNoQuery(writer, request) {
		return
	}
	access, ok := bearerToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	}
	authorization, err := handler.oj.AuthorizeUpload(request.Context(), access, oj.UploadSubmission)
	if err != nil {
		handler.handleOJError(writer, request, err)
		return
	}
	reader, err := beginOJMultipart(writer, request, maximumOJSubmissionRequestBytes(handler.ojPolicy))
	if err != nil {
		handler.handleOJMultipartError(writer, request, err)
		return
	}
	metadataPart, err := nextOJPart(reader, "metadata", "application/json", false)
	if err != nil {
		handler.handleOJMultipartError(writer, request, err)
		return
	}
	var metadata oj.SubmissionMetadata
	if err := decodeOJMetadataPart(metadataPart, maxOJSubmissionMetadataBytes, &metadata); err != nil {
		handler.handleOJMultipartError(writer, request, err)
		return
	}
	sourcePart, err := nextOJPart(reader, "source", oj.CPP20SourceMediaType, true)
	if err != nil {
		handler.handleOJMultipartError(writer, request, err)
		return
	}
	source, err := readOJBinaryPart(sourcePart, handler.ojPolicy.MaximumSourceBytes)
	if err != nil {
		handler.handleOJMultipartError(writer, request, err)
		return
	}
	var stdin []byte
	switch metadata.Mode {
	case oj.SubmissionRun:
		stdinPart, err := nextOJPart(reader, "stdin", oj.PlainTextMediaType, true)
		if err != nil {
			handler.handleOJMultipartError(writer, request, err)
			return
		}
		stdin, err = readOJBinaryPart(stdinPart, handler.ojPolicy.MaximumStdinBytes)
		if err != nil {
			handler.handleOJMultipartError(writer, request, err)
			return
		}
	case oj.SubmissionSubmit:
	default:
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_oj_submission", "OJ submission mode is invalid.")
		return
	}
	if err := requireOJMultipartEOF(reader); err != nil {
		handler.handleOJMultipartError(writer, request, err)
		return
	}
	if err := reader.finish(request); err != nil {
		handler.handleOJMultipartError(writer, request, err)
		return
	}
	var stdinReader io.Reader
	if metadata.Mode == oj.SubmissionRun {
		stdinReader = bytes.NewReader(stdin)
	}
	result, err := handler.oj.CreateSubmission(request.Context(), authorization, metadata, bytes.NewReader(source), stdinReader)
	if err != nil {
		handler.handleOJError(writer, request, err)
		return
	}
	writer.Header().Set("Location", "/api/v2/oj/submissions/"+result.Submission.ID)
	writeJSON(writer, http.StatusAccepted, result)
}

func (handler *Handler) getOJSubmission(writer http.ResponseWriter, request *http.Request) {
	if !requestBodyIsEmpty(request) || !handler.requireNoQuery(writer, request) {
		if !requestBodyIsEmpty(request) {
			handler.writeAPIError(writer, request, http.StatusBadRequest, "request_body_not_allowed", "Request body must be empty.")
		}
		return
	}
	submissionID := request.PathValue("submissionId")
	if !oj.ValidPublicID(submissionID) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_oj_submission_id", "OJ submission ID is invalid.")
		return
	}
	access, ok := bearerToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	}
	detail, found, err := handler.oj.GetSubmission(request.Context(), access, submissionID)
	if err != nil {
		handler.handleOJError(writer, request, err)
		return
	}
	if !found {
		handler.writeAPIError(writer, request, http.StatusNotFound, "oj_submission_not_found", "OJ submission does not exist.")
		return
	}
	writeJSON(writer, http.StatusOK, detail)
}

func (handler *Handler) streamOJJudgeEvents(writer http.ResponseWriter, request *http.Request) {
	if !requestBodyIsEmpty(request) || !handler.requireNoQuery(writer, request) {
		if !requestBodyIsEmpty(request) {
			handler.writeAPIError(writer, request, http.StatusBadRequest, "request_body_not_allowed", "Request body must be empty.")
		}
		return
	}
	submissionID := request.PathValue("submissionId")
	if !oj.ValidPublicID(submissionID) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_oj_submission_id", "OJ submission ID is invalid.")
		return
	}
	after, err := parseLastEventID(request.Header)
	if err != nil {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_last_event_id", "Last-Event-ID must be one canonical non-negative decimal sequence.")
		return
	}
	access, ok := bearerToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	}
	if !handler.acquireSSE(writer, request) {
		return
	}
	defer handler.releaseSSE()
	streamDeadline := time.Now().Add(handler.sseMaxDuration)
	streamContext, cancelStream := context.WithDeadline(request.Context(), streamDeadline)
	defer cancelStream()
	request = request.WithContext(streamContext)
	batch, found, err := handler.oj.ReadJudgeEvents(request.Context(), access, submissionID, after, ojEventBatchSize)
	if err != nil {
		handler.handleOJError(writer, request, err)
		return
	}
	if !found {
		handler.writeAPIError(writer, request, http.StatusNotFound, "oj_submission_not_found", "OJ submission does not exist.")
		return
	}
	controller := http.NewResponseController(writer)
	if err := clearSSEWriteDeadline(controller); err != nil {
		handler.writeAPIError(writer, request, http.StatusInternalServerError, "internal_error", "Request could not be completed.")
		return
	}
	stopWriteInterrupt := installSSEWriteInterrupt(request.Context(), controller)
	defer stopWriteInterrupt()
	if err := handler.setSSEWriteDeadline(controller, streamDeadline); err != nil {
		handler.handleSSESetupError(writer, request, err)
		return
	}
	header := writer.Header()
	header.Set("Content-Type", "text/event-stream; charset=utf-8")
	header.Set("Cache-Control", "no-store")
	header.Set("X-Accel-Buffering", "no")
	header.Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(http.StatusOK)
	if err := controller.Flush(); err != nil {
		return
	}
	if err := clearSSEWriteDeadline(controller); err != nil {
		return
	}
	heartbeat := time.NewTimer(ojEventHeartbeat)
	defer heartbeat.Stop()
	for {
		if len(batch.Events) > 0 {
			if err := handler.setSSEWriteDeadline(controller, streamDeadline); err != nil {
				return
			}
			for _, event := range batch.Events {
				if err := writeOJEvent(writer, event); err != nil {
					return
				}
				after = event.Sequence
			}
			if err := controller.Flush(); err != nil {
				return
			}
			if err := clearSSEWriteDeadline(controller); err != nil {
				return
			}
		}
		if len(batch.Events) == ojEventBatchSize {
			batch, found, err = handler.oj.ReadJudgeEvents(request.Context(), access, submissionID, after, ojEventBatchSize)
			if err != nil || !found {
				handler.logOJStreamFailure(request, err, found)
				return
			}
			continue
		}
		if batch.Terminal {
			return
		}
		poll := time.NewTimer(ojEventPollInterval)
		select {
		case <-request.Context().Done():
			poll.Stop()
			return
		case <-heartbeat.C:
			poll.Stop()
			if err := handler.setSSEWriteDeadline(controller, streamDeadline); err != nil {
				return
			}
			if _, err := fmt.Fprint(writer, ": keep-alive\n\n"); err != nil {
				return
			}
			if err := controller.Flush(); err != nil {
				return
			}
			if err := clearSSEWriteDeadline(controller); err != nil {
				return
			}
			heartbeat.Reset(ojEventHeartbeat)
		case <-poll.C:
		}
		batch, found, err = handler.oj.ReadJudgeEvents(request.Context(), access, submissionID, after, ojEventBatchSize)
		if err != nil || !found {
			handler.logOJStreamFailure(request, err, found)
			return
		}
	}
}

func beginOJMultipart(writer http.ResponseWriter, request *http.Request, maximumBytes int64) (*ojMultipartReader, error) {
	contentType, present, valid := singleHeader(request.Header, "Content-Type")
	if !valid || !present {
		return nil, &requestContractError{status: http.StatusUnsupportedMediaType, code: "unsupported_media_type", message: "Content-Type must be multipart/form-data."}
	}
	mediaType, parameters, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != "multipart/form-data" || parameters["boundary"] == "" || len(parameters) != 1 {
		return nil, &requestContractError{status: http.StatusUnsupportedMediaType, code: "unsupported_media_type", message: "Content-Type must be multipart/form-data with one boundary."}
	}
	if len(request.Header.Values("Content-Encoding")) != 0 {
		return nil, &requestContractError{status: http.StatusUnsupportedMediaType, code: "unsupported_content_encoding", message: "Content-Encoding is not supported."}
	}
	if request.ContentLength > maximumBytes {
		return nil, &requestContractError{status: http.StatusRequestEntityTooLarge, code: "payload_too_large", message: "OJ upload exceeds its hard byte limit."}
	}
	limited := http.MaxBytesReader(unwrapResponseWriter(writer), request.Body, maximumBytes)
	entity := &trackedOJMultipartBody{
		source:      limited,
		finalMarker: []byte("\r\n--" + parameters["boundary"] + "--\r\n"),
	}
	return &ojMultipartReader{Reader: multipart.NewReader(entity, parameters["boundary"]), entity: entity}, nil
}

type ojMultipartReader struct {
	Reader *multipart.Reader
	entity *trackedOJMultipartBody
}

func (reader *ojMultipartReader) NextPart() (*multipart.Part, error) {
	return reader.Reader.NextPart()
}

func (reader *ojMultipartReader) finish(request *http.Request) error {
	if !reader.entity.finalSeen || reader.entity.trailing {
		return &requestContractError{status: http.StatusBadRequest, code: "invalid_multipart", message: "OJ multipart request must end at its final boundary."}
	}
	var probe [1]byte
	emptyReads := 0
	for {
		count, err := reader.entity.Read(probe[:])
		if count > 0 {
			return &requestContractError{status: http.StatusBadRequest, code: "invalid_multipart", message: "OJ multipart request contains bytes after its final boundary."}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return finishRequestBodyRead(request)
			}
			return multipartContractError(err)
		}
		emptyReads++
		if emptyReads >= 100 {
			return &requestContractError{status: http.StatusBadRequest, code: "invalid_multipart", message: "OJ multipart body made no read progress."}
		}
	}
}

type trackedOJMultipartBody struct {
	source      io.Reader
	finalMarker []byte
	scanTail    []byte
	finalSeen   bool
	trailing    bool
}

func (body *trackedOJMultipartBody) Read(buffer []byte) (int, error) {
	count, err := body.source.Read(buffer)
	if count <= 0 {
		return count, err
	}
	if body.finalSeen {
		body.trailing = true
		return count, err
	}
	combined := make([]byte, len(body.scanTail)+count)
	copy(combined, body.scanTail)
	copy(combined[len(body.scanTail):], buffer[:count])
	if index := bytes.Index(combined, body.finalMarker); index >= 0 {
		body.finalSeen = true
		body.trailing = index+len(body.finalMarker) != len(combined)
		body.scanTail = nil
		return count, err
	}
	keep := len(body.finalMarker) - 1
	if keep > len(combined) {
		keep = len(combined)
	}
	body.scanTail = append(body.scanTail[:0], combined[len(combined)-keep:]...)
	return count, err
}

func nextOJPart(reader *ojMultipartReader, name, mediaType string, requireFile bool) (*multipart.Part, error) {
	part, err := reader.NextPart()
	if err != nil {
		return nil, multipartContractError(err)
	}
	disposition, present, valid := singleHeader(http.Header(part.Header), "Content-Disposition")
	dispositionType, dispositionParameters, dispositionErr := mime.ParseMediaType(disposition)
	expectedParameters := 1
	if requireFile {
		expectedParameters = 2
	}
	filename := dispositionParameters["filename"]
	if len(part.Header) != 2 || !valid || !present || dispositionErr != nil || dispositionType != "form-data" ||
		len(dispositionParameters) != expectedParameters || dispositionParameters["name"] != name ||
		requireFile != (filename != "") || requireFile && !validOJFilename(filename) {
		return nil, &requestContractError{status: http.StatusBadRequest, code: "invalid_multipart", message: "OJ multipart parts do not match the closed contract."}
	}
	contentType, present, valid := singleHeader(http.Header(part.Header), "Content-Type")
	if !valid || !present || contentType != mediaType {
		return nil, &requestContractError{status: http.StatusUnsupportedMediaType, code: "unsupported_media_type", message: "OJ multipart part media type is invalid."}
	}
	return part, nil
}

func validOJFilename(value string) bool {
	return value == strings.TrimSpace(value) && len(value) >= 1 && len(value) <= 255 && utf8.ValidString(value) &&
		strings.IndexByte(value, 0) < 0 && !strings.ContainsAny(value, `/\`)
}

func decodeOJMetadataPart(part *multipart.Part, maximumBytes int64, destination any) error {
	body, err := io.ReadAll(io.LimitReader(part, maximumBytes+1))
	if err != nil {
		return multipartContractError(err)
	}
	if int64(len(body)) > maximumBytes {
		return &requestContractError{status: http.StatusRequestEntityTooLarge, code: "payload_too_large", message: "OJ metadata exceeds its hard byte limit."}
	}
	return decodeStrictJSONDocument(body, destination)
}

func readOJBinaryPart(part *multipart.Part, maximumBytes int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(part, maximumBytes+1))
	if err != nil {
		return nil, multipartContractError(err)
	}
	if len(body) == 0 {
		return nil, &requestContractError{status: http.StatusBadRequest, code: "invalid_multipart", message: "OJ binary part must be non-empty."}
	}
	if int64(len(body)) > maximumBytes {
		return nil, &requestContractError{status: http.StatusRequestEntityTooLarge, code: "payload_too_large", message: "OJ binary part exceeds its hard byte limit."}
	}
	return body, nil
}

func requireOJMultipartEOF(reader *ojMultipartReader) error {
	_, err := reader.NextPart()
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return &requestContractError{status: http.StatusBadRequest, code: "invalid_multipart", message: "OJ multipart request contains unexpected parts."}
	}
	return multipartContractError(err)
}

type finalOJMultipartPartReader struct {
	part        *multipart.Part
	reader      *ojMultipartReader
	readContext context.Context
	request     *http.Request
	checked     bool
}

func (reader *finalOJMultipartPartReader) Read(buffer []byte) (int, error) {
	count, err := reader.part.Read(buffer)
	if cause := context.Cause(reader.readContext); cause != nil {
		return count, cause
	}
	if !errors.Is(err, io.EOF) {
		if err != nil {
			return count, multipartContractError(err)
		}
		return count, nil
	}
	if reader.checked {
		return count, io.EOF
	}
	reader.checked = true
	if closeErr := reader.part.Close(); closeErr != nil {
		return count, closeErr
	}
	_, nextErr := reader.reader.NextPart()
	if cause := context.Cause(reader.readContext); cause != nil {
		return count, cause
	}
	if errors.Is(nextErr, io.EOF) {
		if finishErr := reader.reader.finish(reader.request); finishErr != nil {
			return count, finishErr
		}
		return count, io.EOF
	}
	if nextErr == nil {
		return count, errUnexpectedOJMultipartPart
	}
	return count, nextErr
}

func (handler *Handler) handleOJMultipartError(writer http.ResponseWriter, request *http.Request, err error) {
	if cause := context.Cause(requestBodyReadContext(request)); cause != nil {
		handler.handleRequestBodyError(writer, request, cause, "OJ upload exceeded its duration limit.")
		return
	}
	handler.handleRequestContractError(writer, request, err)
}

func multipartContractError(err error) error {
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		return &requestContractError{status: http.StatusRequestEntityTooLarge, code: "payload_too_large", message: "OJ upload exceeds its hard byte limit."}
	}
	return &requestContractError{status: http.StatusBadRequest, code: "invalid_multipart", message: "OJ multipart body is invalid."}
}

func writeOJEvent(writer http.ResponseWriter, event oj.JudgeEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, data); err != nil {
		return err
	}
	_, err = fmt.Fprintf(writer, "id: %d\nevent: %s\ndata: %s\n\n", event.Sequence, event.Type, compact.Bytes())
	return err
}

func (handler *Handler) handleOJError(writer http.ResponseWriter, request *http.Request, err error) {
	if auth.ErrorCodeOf(err) != "" {
		handler.handleAuthError(writer, request, err)
		return
	}
	if errors.Is(err, errUnexpectedOJMultipartPart) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_multipart", "OJ multipart request contains unexpected parts.")
		return
	}
	var contractError *requestContractError
	if errors.As(err, &contractError) {
		handler.handleOJMultipartError(writer, request, contractError)
		return
	}
	switch oj.CodeOf(err) {
	case oj.ErrorInvalidInput:
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_oj_request", "OJ request is invalid.")
		return
	case oj.ErrorPayloadTooLarge:
		handler.writeAPIError(writer, request, http.StatusRequestEntityTooLarge, "payload_too_large", "OJ upload exceeds its hard byte limit.")
		return
	case oj.ErrorPrincipalRejected:
		handler.writeAPIError(writer, request, http.StatusForbidden, "auth_forbidden", "Authorization was rejected.")
		return
	case oj.ErrorNotFound:
		handler.writeAPIError(writer, request, http.StatusNotFound, "oj_not_found", "OJ resource does not exist.")
		return
	case oj.ErrorHeadConflict:
		handler.writeAPIError(writer, request, http.StatusConflict, "oj_head_conflict", "OJ problem head changed concurrently.")
		return
	case oj.ErrorIdempotencyConflict:
		handler.writeAPIError(writer, request, http.StatusConflict, "oj_idempotency_conflict", "OJ request identity conflicts with stored input.")
		return
	case oj.ErrorArtifactConflict:
		handler.writeAPIError(writer, request, http.StatusConflict, "oj_artifact_conflict", "OJ artifact metadata conflicts with stored content.")
		return
	case oj.ErrorCanceled:
		if errors.Is(context.Cause(requestBodyReadContext(request)), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			handler.writeAPIError(writer, request, http.StatusRequestTimeout, "request_timeout", "OJ request exceeded its duration limit.")
			return
		}
		if errors.Is(err, context.Canceled) {
			handler.writeAPIError(writer, request, http.StatusBadRequest, "request_canceled", "Request was canceled.")
			return
		}
	}
	handler.logger.ErrorContext(request.Context(), "OJ HTTP operation failed", "request_id", requestID(request.Context()), "code", oj.CodeOf(err))
	handler.writeAPIError(writer, request, http.StatusInternalServerError, "internal_error", "Request could not be completed.")
}

func (handler *Handler) logOJStreamFailure(request *http.Request, err error, found bool) {
	handler.logger.InfoContext(request.Context(), "OJ event stream ended", "request_id", requestID(request.Context()),
		"found", found, "code", oj.CodeOf(err))
}
