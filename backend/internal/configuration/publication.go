package configuration

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
)

// VersionWriteTransaction is the read surface available to a specialized
// configuration publication precondition. The repository owns transaction
// lifecycle and invokes the precondition before it writes a version.
type VersionWriteTransaction interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

// VersionWritePrecondition extends the generic immutable configuration store
// with domain-owned checks that must share its database transaction.
type VersionWritePrecondition interface {
	ValidateVersionWrite(context.Context, VersionWriteTransaction, CreateVersionCommand) error
}

type PublicationIssue struct {
	IssueCode                          string
	ProblemKeys                        []string
	MissingProblemKeys                 []string
	DanglingProblemKeys                []string
	ExpectedAnalyticsGenerationID      string
	CurrentAnalyticsGenerationID       string
	ExpectedAnalyticsHeadRevision      int64
	CurrentAnalyticsHeadRevision       int64
	ExpectedInputManifestSHA256        string
	CurrentInputManifestSHA256         string
	ExpectedCurrentModelHeadRevision   int64
	CurrentModelHeadRevision           int64
	ExpectedCurrentModelArtifactSHA256 string
	CurrentModelArtifactSHA256         string
}

func (issue *PublicationIssue) Error() string {
	if issue == nil {
		return "<nil>"
	}
	return fmt.Sprintf("configuration publication precondition %s failed", issue.IssueCode)
}

func PublicationIssueOf(err error) (PublicationIssue, bool) {
	var issue *PublicationIssue
	if !errors.As(err, &issue) {
		return PublicationIssue{}, false
	}
	result := *issue
	result.ProblemKeys = slices.Clone(issue.ProblemKeys)
	result.MissingProblemKeys = slices.Clone(issue.MissingProblemKeys)
	result.DanglingProblemKeys = slices.Clone(issue.DanglingProblemKeys)
	return result, true
}
