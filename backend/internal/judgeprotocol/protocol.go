// Package judgeprotocol defines the only byte protocol between ascendanyd and
// one isolated ascendany-judge instance. The protocol is deliberately small:
// a canonical JSON header followed by exact-length, SHA-256-bound payloads.
package judgeprotocol

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
	"github.com/kkkzbh/AscendAny/backend/internal/judgecontract"
)

const (
	RequestSchemaV1  = "ascendany.judge-control.request.v1"
	ResponseSchemaV1 = "ascendany.judge-control.response.v1"
	ResultSchemaV1   = "ascendany.judge-control.result.v1"

	requestMagic  = "AAJREQ01"
	responseMagic = "AAJRES01"
	headerLimit   = 1 << 20
	detailLimit   = 4096
)

type Artifact struct {
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"sizeBytes"`
}

type RequestHeader struct {
	Schema           string                       `json:"schema"`
	JudgeJobID       string                       `json:"judgeJobId"`
	SubmissionID     string                       `json:"submissionId"`
	ProblemID        string                       `json:"problemId"`
	ProblemVersion   int64                        `json:"problemVersion"`
	Mode             judgecontract.SubmissionMode `json:"mode"`
	LanguageID       string                       `json:"languageId"`
	Source           Artifact                     `json:"source"`
	Stdin            *Artifact                    `json:"stdin"`
	TestBundle       Artifact                     `json:"testBundle"`
	ProblemSchema    string                       `json:"problemSchema"`
	ProblemSpec      json.RawMessage              `json:"problemSpec"`
	TimeLimitMS      int                          `json:"timeLimitMs"`
	MemoryLimitBytes int64                        `json:"memoryLimitBytes"`
	OutputLimitBytes int64                        `json:"outputLimitBytes"`
}

type Result struct {
	Schema          string                `json:"schema"`
	Verdict         judgecontract.Verdict `json:"verdict"`
	ScoreFraction   float64               `json:"scoreFraction"`
	PassedCaseCount int64                 `json:"passedCaseCount"`
	TotalCaseCount  int64                 `json:"totalCaseCount"`
	MaxTimeMS       int64                 `json:"maxTimeMs"`
	MaxMemoryBytes  int64                 `json:"maxMemoryBytes"`
	ResultManifest  json.RawMessage       `json:"resultManifest"`
	OutputSHA256    *string               `json:"outputSha256"`
	OutputSizeBytes int64                 `json:"outputSizeBytes"`
}

type Failure struct {
	Code      string `json:"code"`
	Permanent bool   `json:"permanent"`
	Detail    string `json:"detail"`
}

type ResponseHeader struct {
	Schema  string   `json:"schema"`
	JobID   string   `json:"jobId"`
	Result  *Result  `json:"result"`
	Failure *Failure `json:"failure"`
}

type Payloads struct {
	Source     io.Reader
	Stdin      io.Reader
	TestBundle io.Reader
}

type PayloadKind string

const (
	PayloadSource     PayloadKind = "source"
	PayloadStdin      PayloadKind = "stdin"
	PayloadTestBundle PayloadKind = "test_bundle"
)

// PayloadConsumer must consume content to EOF during the callback. The reader
// exposes exactly the bytes declared by artifact.
type PayloadConsumer func(header RequestHeader, kind PayloadKind, artifact Artifact, content io.Reader) error
type HeaderValidator func(RequestHeader) error

func HeaderFromExecution(request judgecontract.ExecutionRequest) RequestHeader {
	return RequestHeader{
		Schema: RequestSchemaV1, JudgeJobID: request.JudgeJobID,
		SubmissionID: request.SubmissionID, ProblemID: request.ProblemID,
		ProblemVersion: request.ProblemVersion, Mode: request.Mode,
		LanguageID: request.LanguageID, Source: artifactFromExecution(request.Source),
		Stdin: artifactPointerFromExecution(request.Stdin), TestBundle: artifactFromExecution(request.TestBundle),
		ProblemSchema: request.ProblemSchema, ProblemSpec: request.ProblemSpec,
		TimeLimitMS: request.TimeLimitMS, MemoryLimitBytes: request.MemoryLimitBytes,
		OutputLimitBytes: request.OutputLimitBytes,
	}
}

func WriteRequest(writer io.Writer, header RequestHeader, payloads Payloads) error {
	if writer == nil || payloads.Source == nil || payloads.TestBundle == nil || (header.Stdin != nil) != (payloads.Stdin != nil) {
		return errors.New("judge protocol request writer and payloads are incomplete")
	}
	if err := writeHeader(writer, requestMagic, header); err != nil {
		return fmt.Errorf("write request header: %w", err)
	}
	for _, payload := range []struct {
		artifact Artifact
		reader   io.Reader
	}{
		{header.Source, payloads.Source},
		{artifactValue(header.Stdin), payloads.Stdin},
		{header.TestBundle, payloads.TestBundle},
	} {
		if payload.reader == nil {
			continue
		}
		if err := copyExactAndHash(writer, payload.reader, payload.artifact); err != nil {
			return fmt.Errorf("write request payload: %w", err)
		}
	}
	return nil
}

func ReadRequest(reader io.Reader, validate HeaderValidator, consume PayloadConsumer) (RequestHeader, error) {
	var header RequestHeader
	if reader == nil || validate == nil || consume == nil {
		return header, errors.New("judge protocol request reader, validator, and consumer are required")
	}
	if err := readHeader(reader, requestMagic, &header); err != nil {
		return header, fmt.Errorf("read request header: %w", err)
	}
	if err := validate(header); err != nil {
		return header, fmt.Errorf("validate request header: %w", err)
	}
	var resultErr error
	for _, payload := range []struct {
		kind     PayloadKind
		artifact *Artifact
	}{
		{PayloadSource, &header.Source},
		{PayloadStdin, header.Stdin},
		{PayloadTestBundle, &header.TestBundle},
	} {
		if payload.artifact == nil {
			continue
		}
		consumer := consume
		if resultErr != nil {
			consumer = func(RequestHeader, PayloadKind, Artifact, io.Reader) error { return nil }
		}
		if err := consumeExactAndHash(reader, header, payload.kind, *payload.artifact, consumer); err != nil && resultErr == nil {
			resultErr = fmt.Errorf("read %s payload: %w", payload.kind, err)
		}
	}
	return header, resultErr
}

func WriteResponse(writer io.Writer, header ResponseHeader, output []byte) error {
	if writer == nil {
		return errors.New("judge protocol response writer is required")
	}
	if (header.Result == nil) == (header.Failure == nil) || (header.Failure != nil && len(output) != 0) {
		return errors.New("judge protocol response must contain exactly one result or failure")
	}
	if header.Result != nil {
		digest := sha256.Sum256(output)
		encoded := hex.EncodeToString(digest[:])
		if len(output) == 0 {
			header.Result.OutputSHA256 = nil
			header.Result.OutputSizeBytes = 0
		} else {
			header.Result.OutputSHA256 = &encoded
			header.Result.OutputSizeBytes = int64(len(output))
		}
	}
	if err := writeHeader(writer, responseMagic, header); err != nil {
		return fmt.Errorf("write response header: %w", err)
	}
	if len(output) > 0 {
		if _, err := writer.Write(output); err != nil {
			return fmt.Errorf("write response output: %w", err)
		}
	}
	return nil
}

func ReadResponse(reader io.Reader, maximumOutputBytes int64) (ResponseHeader, []byte, error) {
	var header ResponseHeader
	if reader == nil || maximumOutputBytes < 1 {
		return header, nil, errors.New("judge protocol response reader and positive output limit are required")
	}
	if err := readHeader(reader, responseMagic, &header); err != nil {
		return header, nil, fmt.Errorf("read response header: %w", err)
	}
	if header.Result == nil {
		return header, nil, nil
	}
	if header.Result.OutputSizeBytes == 0 {
		if header.Result.OutputSHA256 != nil {
			return header, nil, errors.New("empty response output has an unexpected digest")
		}
		return header, nil, nil
	}
	if header.Result.OutputSizeBytes < 0 || header.Result.OutputSizeBytes > maximumOutputBytes || header.Result.OutputSHA256 == nil {
		return header, nil, errors.New("response output descriptor exceeds the negotiated limit")
	}
	output := make([]byte, header.Result.OutputSizeBytes)
	if _, err := io.ReadFull(reader, output); err != nil {
		return header, nil, fmt.Errorf("read response output: %w", err)
	}
	digest := sha256.Sum256(output)
	if hex.EncodeToString(digest[:]) != *header.Result.OutputSHA256 {
		return header, nil, errors.New("response output digest mismatch")
	}
	return header, output, nil
}

func writeHeader(writer io.Writer, magic string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	canonical, _, err := canonicaljson.Object(raw, headerLimit)
	if err != nil {
		return err
	}
	if len(canonical) > headerLimit {
		return errors.New("protocol header exceeds hard limit")
	}
	prefix := make([]byte, len(magic)+4)
	copy(prefix, magic)
	binary.BigEndian.PutUint32(prefix[len(magic):], uint32(len(canonical)))
	if _, err := writer.Write(prefix); err != nil {
		return err
	}
	_, err = writer.Write(canonical)
	return err
}

func readHeader(reader io.Reader, magic string, destination any) error {
	prefix := make([]byte, len(magic)+4)
	if _, err := io.ReadFull(reader, prefix); err != nil {
		return err
	}
	if string(prefix[:len(magic)]) != magic {
		return errors.New("protocol magic mismatch")
	}
	length := binary.BigEndian.Uint32(prefix[len(magic):])
	if length < 2 || length > headerLimit {
		return errors.New("protocol header length is invalid")
	}
	raw := make([]byte, length)
	if _, err := io.ReadFull(reader, raw); err != nil {
		return err
	}
	canonical, _, err := canonicaljson.Object(raw, headerLimit)
	if err != nil {
		return err
	}
	if !bytes.Equal(raw, canonical) {
		return errors.New("protocol header is not canonical JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return err
	}
	return nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("unexpected trailing JSON value")
		}
		return err
	}
	return nil
}

func copyExactAndHash(destination io.Writer, source io.Reader, artifact Artifact) error {
	if !validArtifact(artifact) {
		return errors.New("payload descriptor is invalid")
	}
	digest := sha256.New()
	written, err := io.CopyN(io.MultiWriter(destination, digest), source, artifact.SizeBytes)
	if err != nil {
		return fmt.Errorf("payload ended after %d of %d bytes: %w", written, artifact.SizeBytes, err)
	}
	var extra [1]byte
	if _, err := io.ReadFull(source, extra[:]); err == nil {
		return errors.New("payload exceeds its declared size")
	} else if !errors.Is(err, io.EOF) {
		return fmt.Errorf("verify payload trailer: %w", err)
	}
	if hex.EncodeToString(digest.Sum(nil)) != artifact.SHA256 {
		return errors.New("payload digest mismatch")
	}
	return nil
}

func consumeExactAndHash(reader io.Reader, header RequestHeader, kind PayloadKind, artifact Artifact, consume PayloadConsumer) error {
	if !validArtifact(artifact) {
		return errors.New("payload descriptor is invalid")
	}
	limited := &io.LimitedReader{R: reader, N: artifact.SizeBytes}
	digest := sha256.New()
	content := io.TeeReader(limited, digest)
	consumeErr := consume(header, kind, artifact, content)
	unread := limited.N
	if _, err := io.Copy(io.Discard, content); err != nil {
		return err
	}
	if limited.N != 0 {
		return fmt.Errorf("payload is truncated by %d bytes", limited.N)
	}
	if hex.EncodeToString(digest.Sum(nil)) != artifact.SHA256 {
		return errors.New("payload digest mismatch")
	}
	if consumeErr != nil {
		return consumeErr
	}
	if unread != 0 {
		return fmt.Errorf("payload consumer left %d bytes unread", unread)
	}
	return nil
}

func validArtifact(value Artifact) bool {
	if value.SizeBytes < 1 || len(value.SHA256) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(value.SHA256)
	return err == nil && len(decoded) == sha256.Size
}

func artifactFromExecution(value judgecontract.Artifact) Artifact {
	return Artifact{SHA256: value.SHA256, SizeBytes: value.SizeBytes}
}

func artifactPointerFromExecution(value *judgecontract.Artifact) *Artifact {
	if value == nil {
		return nil
	}
	converted := artifactFromExecution(*value)
	return &converted
}

func artifactValue(value *Artifact) Artifact {
	if value == nil {
		return Artifact{}
	}
	return *value
}

func ValidFailure(failure *Failure) bool {
	return failure != nil && len(failure.Code) >= 1 && len(failure.Code) <= 64 &&
		len(failure.Detail) >= 1 && len(failure.Detail) <= detailLimit
}
