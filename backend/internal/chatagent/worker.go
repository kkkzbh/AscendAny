package chatagent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
	"github.com/kkkzbh/AscendAny/backend/internal/workerlease"
)

var schemaIDPattern = regexp.MustCompile(`^ascendany[.][a-z][a-z0-9_.-]{0,126}[.]v[1-9][0-9]*$`)

type WorkerConfig struct {
	Owner               string
	LeaseDuration       time.Duration
	MaximumContextItems int
	MaximumToolRounds   int
}

type Worker struct {
	repository          WorkerRepository
	provider            Provider
	tools               ToolExecutor
	owner               string
	leaseDuration       time.Duration
	maximumContextItems int
	maximumToolRounds   int
	uuid                UUIDGenerator
	clock               func() time.Time
}

func NewWorker(repository WorkerRepository, provider Provider, tools ToolExecutor, config WorkerConfig) (*Worker, error) {
	return newWorker(repository, provider, tools, config, randomUUIDv4, time.Now)
}

func newWorker(
	repository WorkerRepository,
	provider Provider,
	tools ToolExecutor,
	config WorkerConfig,
	uuid UUIDGenerator,
	clock func() time.Time,
) (*Worker, error) {
	if repository == nil || provider == nil || tools == nil || uuid == nil || clock == nil || strings.TrimSpace(config.Owner) != config.Owner ||
		config.Owner == "" || len(config.Owner) > 128 || config.MaximumContextItems < 1 || config.MaximumContextItems > 1000 ||
		config.MaximumToolRounds < 1 || config.MaximumToolRounds > 64 {
		return nil, domainError(ErrorInvalidConfiguration, true, "construct chat agent worker", errors.New("repository, ports, bounded owner, context, and tool rounds are required"))
	}
	if _, err := workerlease.ValidateDuration(config.LeaseDuration); err != nil {
		return nil, domainError(ErrorInvalidConfiguration, true, "construct chat agent worker", err)
	}
	return &Worker{
		repository: repository, provider: provider, tools: tools, owner: config.Owner,
		leaseDuration: config.LeaseDuration, maximumContextItems: config.MaximumContextItems,
		maximumToolRounds: config.MaximumToolRounds, uuid: uuid, clock: clock,
	}, nil
}

func (worker *Worker) RunOne(ctx context.Context) (*WorkerOutcome, error) {
	if ctx == nil {
		return nil, domainError(ErrorInvalidInput, true, "run chat agent worker", errors.New("context is required"))
	}
	attemptToken, err := worker.uuid()
	if err != nil {
		return nil, domainError(ErrorInvalidConfiguration, false, "generate agent attempt token", err)
	}
	claim, err := worker.repository.Claim(ctx, worker.owner, attemptToken, worker.leaseDuration)
	if err != nil || claim == nil {
		return nil, err
	}
	outcome, err := worker.Process(ctx, *claim)
	return &outcome, err
}

func (worker *Worker) Process(ctx context.Context, claim Claim) (WorkerOutcome, error) {
	if ctx == nil {
		return WorkerOutcome{}, domainError(ErrorInvalidInput, true, "process chat agent run", errors.New("context is required"))
	}
	renewer, err := workerlease.Start(ctx, worker.leaseDuration, func(renewContext context.Context) error {
		return worker.repository.RenewLease(renewContext, claim, worker.leaseDuration)
	})
	if err != nil {
		return WorkerOutcome{}, err
	}
	outcome, resultErr := worker.processWithLease(renewer.Context(), claim)
	renewer.Stop()
	if renewalErr := renewer.Failure(); renewalErr != nil {
		if resultErr == nil && outcome.Disposition != "" && CodeOf(renewalErr) == ErrorLeaseLost {
			return outcome, nil
		}
		return WorkerOutcome{}, renewalErr
	}
	return outcome, resultErr
}

func (worker *Worker) processWithLease(ctx context.Context, claim Claim) (WorkerOutcome, error) {
	work, err := worker.repository.LoadWork(ctx, claim, worker.maximumContextItems)
	if err != nil {
		if CodeOf(err) == ErrorStoredDataInvalid {
			return worker.failRun(ctx, claim, "stored_data_invalid", "agent work snapshot is invalid")
		}
		return WorkerOutcome{}, err
	}
	request := ProviderRequest{
		RunID: work.RunID, Kind: work.Kind, ThreadID: work.ThreadID, StudentNumber: work.StudentNumber,
		Analytics: work.Analytics, Prompt: work.Prompt, Model: work.Model,
		Conversation: append([]Message(nil), work.Conversation...), ToolCalls: append([]ToolCallRecord(nil), work.ToolCalls...),
	}
	maximumToolCalls := worker.maximumToolRounds * MaxProviderToolCallsPerTurn
	if len(work.ToolCalls) > maximumToolCalls {
		return worker.failRun(ctx, claim, "tool_call_limit", "stored tool calls exceed the configured run limit")
	}
	existing := make(map[string]ToolCallRecord, len(work.ToolCalls))
	for _, call := range work.ToolCalls {
		existing[call.Key] = call
	}
	for round := 0; round <= worker.maximumToolRounds; round++ {
		response, err := worker.provider.Generate(ctx, request)
		if contextErr := context.Cause(ctx); contextErr != nil {
			return WorkerOutcome{}, domainError(ErrorCanceled, false, "generate agent response", contextErr)
		}
		if err != nil {
			code := "provider_failure"
			detail := "provider execution failed"
			var failure *ProviderFailure
			if errors.As(err, &failure) && identifierPattern.MatchString(failure.Code) &&
				strings.TrimSpace(failure.Detail) == failure.Detail && failure.Detail != "" &&
				len(failure.Detail) <= MaxFailureDetailBytes && utf8.ValidString(failure.Detail) {
				code = failure.Code
				detail = failure.Detail
			}
			return worker.failRun(ctx, claim, code, detail)
		}
		if response.Assistant != nil {
			if len(response.ToolCalls) != 0 || validateAssistantOutput(*response.Assistant) != nil {
				return worker.failRun(ctx, claim, "provider_contract_invalid", "provider returned an invalid assistant response")
			}
			messageID, err := worker.uuid()
			if err != nil {
				return WorkerOutcome{}, domainError(ErrorInvalidConfiguration, false, "generate assistant message ID", err)
			}
			if err := worker.repository.Complete(ctx, claim, Completion{MessageID: messageID, Output: *response.Assistant}); err != nil {
				return WorkerOutcome{}, err
			}
			return WorkerOutcome{RunID: claim.ID, Disposition: WorkerSucceeded, MessageID: &messageID}, nil
		}
		if len(response.ToolCalls) == 0 || len(response.ToolCalls) > MaxProviderToolCallsPerTurn {
			return worker.failRun(ctx, claim, "provider_contract_invalid", "provider returned neither one assistant response nor bounded tool calls")
		}
		if round == worker.maximumToolRounds {
			return worker.failRun(ctx, claim, "tool_round_limit", "provider exceeded the configured tool round limit")
		}
		seenThisTurn := make(map[string]struct{}, len(response.ToolCalls))
		for _, call := range response.ToolCalls {
			normalized, err := normalizeProviderToolCall(call)
			if err != nil {
				return worker.failRun(ctx, claim, "provider_contract_invalid", truncateDetail(err.Error()))
			}
			if _, duplicate := seenThisTurn[normalized.Key]; duplicate {
				return worker.failRun(ctx, claim, "provider_contract_invalid", "provider repeated a tool call key in one turn")
			}
			seenThisTurn[normalized.Key] = struct{}{}
			if stored, found := existing[normalized.Key]; found {
				if stored.Name != normalized.Name || stored.ArgumentsSchema != normalized.ArgumentsSchema || stored.ArgumentsSHA256 != normalized.ArgumentsSHA256 ||
					!bytes.Equal(stored.Arguments, normalized.Arguments) {
					return worker.failRun(ctx, claim, "provider_contract_invalid", "provider reused a tool call key with different immutable arguments")
				}
				continue
			}
			if len(existing) >= maximumToolCalls {
				return worker.failRun(ctx, claim, "tool_call_limit", "provider exceeded the configured run tool call limit")
			}
			startedAt := worker.clock().UTC()
			execution, err := worker.tools.Execute(ctx, ToolRequest{
				RunID: work.RunID, StudentNumber: work.StudentNumber, Analytics: work.Analytics,
				Key: normalized.Key, Name: normalized.Name, ArgumentsSchema: normalized.ArgumentsSchema, Arguments: normalized.Arguments,
			})
			finishedAt := worker.clock().UTC()
			if contextErr := context.Cause(ctx); contextErr != nil {
				return WorkerOutcome{}, domainError(ErrorCanceled, false, "execute agent tool", contextErr)
			}
			if err != nil {
				return worker.failRun(ctx, claim, "tool_execution_failure", "tool executor failed")
			}
			record, err := normalizeToolExecution(normalized, execution, startedAt, finishedAt)
			if err != nil {
				return worker.failRun(ctx, claim, "tool_contract_invalid", truncateDetail(err.Error()))
			}
			record, err = worker.repository.RecordToolCall(ctx, claim, record)
			if err != nil {
				return WorkerOutcome{}, err
			}
			existing[record.Key] = record
			request.ToolCalls = append(request.ToolCalls, record)
		}
	}
	return WorkerOutcome{}, domainError(ErrorProvider, true, "process agent tool rounds", errors.New("tool round state exhausted without a terminal transition"))
}

func (worker *Worker) failRun(ctx context.Context, claim Claim, code, detail string) (WorkerOutcome, error) {
	if err := worker.repository.Fail(ctx, claim, code, detail); err != nil {
		return WorkerOutcome{}, err
	}
	return WorkerOutcome{RunID: claim.ID, Disposition: WorkerFailed, FailureCode: &code}, nil
}

func normalizeProviderToolCall(call ProviderToolCall) (ToolCallRecord, error) {
	if !toolCallKeyPattern.MatchString(call.Key) || !identifierPattern.MatchString(call.Name) || !schemaIDPattern.MatchString(call.ArgumentsSchema) {
		return ToolCallRecord{}, errors.New("tool call key, name, or arguments schema is invalid")
	}
	arguments, digest, err := canonicaljson.Object(call.Arguments, MaxToolDocumentBytes)
	if err != nil {
		return ToolCallRecord{}, fmt.Errorf("canonicalize tool arguments: %w", err)
	}
	return ToolCallRecord{
		Key: call.Key, Name: call.Name, ArgumentsSchema: call.ArgumentsSchema,
		Arguments: arguments, ArgumentsSHA256: digest,
	}, nil
}

func normalizeToolExecution(call ToolCallRecord, execution ToolExecution, startedAt, finishedAt time.Time) (ToolCallRecord, error) {
	if finishedAt.Before(startedAt) {
		return ToolCallRecord{}, errors.New("tool execution time moved backwards")
	}
	call.Outcome = execution.Outcome
	call.StartedAt = startedAt
	call.FinishedAt = finishedAt
	switch execution.Outcome {
	case ToolSucceeded:
		if execution.ResultSchema == nil || !schemaIDPattern.MatchString(*execution.ResultSchema) || execution.ErrorCode != nil {
			return ToolCallRecord{}, errors.New("successful tool result metadata is invalid")
		}
		result, digest, err := canonicaljson.Object(execution.Result, MaxToolDocumentBytes)
		if err != nil {
			return ToolCallRecord{}, fmt.Errorf("canonicalize tool result: %w", err)
		}
		call.ResultSchema = execution.ResultSchema
		call.Result = result
		call.ResultSHA256 = &digest
	case ToolFailed, ToolDenied:
		if execution.ResultSchema != nil || len(execution.Result) != 0 || execution.ErrorCode == nil || !identifierPattern.MatchString(*execution.ErrorCode) {
			return ToolCallRecord{}, errors.New("unsuccessful tool result metadata is invalid")
		}
		call.ErrorCode = execution.ErrorCode
	default:
		return ToolCallRecord{}, errors.New("tool outcome is invalid")
	}
	return call, nil
}

func validateAssistantOutput(output AssistantOutput) error {
	if len(output.Content) < 1 || len(output.Content) > MaxMessageBytes ||
		!utf8.ValidString(output.Content) || strings.TrimSpace(output.Content) == "" || strings.IndexByte(output.Content, 0) >= 0 ||
		output.ReasoningContent != nil && (len(*output.ReasoningContent) > MaxReasoningBytes || !utf8.ValidString(*output.ReasoningContent) || strings.IndexByte(*output.ReasoningContent, 0) >= 0) ||
		output.ContextSummary != nil && (len(*output.ContextSummary) > MaxContextSummaryBytes || !utf8.ValidString(*output.ContextSummary) || strings.IndexByte(*output.ContextSummary, 0) >= 0) {
		return errors.New("assistant output violates byte limits")
	}
	return nil
}

func truncateDetail(value string) string {
	value = strings.ToValidUTF8(value, "�")
	value = strings.TrimSpace(value)
	if value == "" {
		value = "unspecified failure"
	}
	if len(value) > MaxFailureDetailBytes {
		value = value[:MaxFailureDetailBytes]
		for !utf8.ValidString(value) {
			value = value[:len(value)-1]
		}
	}
	return value
}
