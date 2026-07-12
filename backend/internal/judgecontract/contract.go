// Package judgecontract owns the capability-free value contract shared by the
// online OJ runtime and each isolated judge process. It must remain free of
// database, artifact-store, authentication, credential, and network clients.
package judgecontract

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
)

const (
	ProblemSchemaV1           = "ascendany.oj.problem.v1"
	ProblemSpecSchemaV1       = "ascendany.oj.problem-spec.v1"
	TestBundleSchemaV1        = "ascendany.oj.test-bundle.v1"
	ExecutionManifestSchemaV1 = "ascendany.oj.execution-manifest.v1"

	TestBundleMediaType  = "application/vnd.ascendany.oj-test-bundle.v1+tar"
	CPP20SourceMediaType = "text/x-c++src; charset=utf-8"
	PlainTextMediaType   = "text/plain; charset=utf-8"
	JudgeOutputMediaType = "application/octet-stream"
	LanguageCPP20        = "cpp20"
)

var (
	canonicalUUIDv4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	lowercaseSHA256 = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type SubmissionMode string

const (
	SubmissionRun    SubmissionMode = "run"
	SubmissionSubmit SubmissionMode = "submit"
)

type Verdict string

const (
	VerdictAccepted            Verdict = "accepted"
	VerdictWrongAnswer         Verdict = "wrong_answer"
	VerdictCompileError        Verdict = "compile_error"
	VerdictRuntimeError        Verdict = "runtime_error"
	VerdictTimeLimitExceeded   Verdict = "time_limit_exceeded"
	VerdictMemoryLimitExceeded Verdict = "memory_limit_exceeded"
	VerdictOutputLimitExceeded Verdict = "output_limit_exceeded"
)

type Artifact struct {
	SHA256     string `json:"sha256"`
	SizeBytes  int64  `json:"sizeBytes"`
	MediaType  string `json:"mediaType"`
	StorageKey string `json:"storageKey"`
}

type ExecutionRequest struct {
	JudgeJobID       string
	SubmissionID     string
	ProblemID        string
	ProblemVersion   int64
	Mode             SubmissionMode
	LanguageID       string
	Source           Artifact
	Stdin            *Artifact
	TestBundle       Artifact
	ProblemSchema    string
	ProblemSpec      json.RawMessage
	TimeLimitMS      int
	MemoryLimitBytes int64
	OutputLimitBytes int64
}

type ExecutorResult struct {
	Verdict         Verdict
	ScoreFraction   float64
	PassedCaseCount int64
	TotalCaseCount  int64
	MaxTimeMS       int64
	MaxMemoryBytes  int64
	Output          []byte
	ResultManifest  json.RawMessage
}

type ExecutionFailure struct {
	Code      string
	Permanent bool
	Cause     error
}

func (failure *ExecutionFailure) Error() string {
	if failure == nil {
		return "<nil>"
	}
	return fmt.Sprintf("OJ executor %s: %v", failure.Code, failure.Cause)
}

func (failure *ExecutionFailure) Unwrap() error {
	if failure == nil {
		return nil
	}
	return failure.Cause
}

func ValidPublicID(value string) bool {
	return canonicalUUIDv4.MatchString(value)
}

func ValidVerdict(value Verdict) bool {
	switch value {
	case VerdictAccepted, VerdictWrongAnswer, VerdictCompileError, VerdictRuntimeError,
		VerdictTimeLimitExceeded, VerdictMemoryLimitExceeded, VerdictOutputLimitExceeded:
		return true
	default:
		return false
	}
}

func ValidateArtifact(value Artifact, mediaType string, maximumBytes int64) error {
	if maximumBytes < 1 || !lowercaseSHA256.MatchString(value.SHA256) || value.SizeBytes < 1 ||
		value.SizeBytes > maximumBytes || value.MediaType != mediaType ||
		value.StorageKey != "sha256/"+value.SHA256[:2]+"/"+value.SHA256 {
		return errors.New("artifact descriptor violates its media contract")
	}
	return nil
}

type Executor interface {
	Execute(context.Context, ExecutionRequest) (ExecutorResult, error)
}
