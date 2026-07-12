package chatagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

type ProviderRequest struct {
	RunID         string
	Kind          RunKind
	ThreadID      string
	StudentNumber string
	Analytics     *AnalyticsSnapshot
	Prompt        ConfigurationSnapshot
	Model         ConfigurationSnapshot
	Conversation  []Message
	ToolCalls     []ToolCallRecord
}

type ProviderToolCall struct {
	Key             string
	Name            string
	ArgumentsSchema string
	Arguments       json.RawMessage
}

type ProviderResponse struct {
	Assistant *AssistantOutput
	ToolCalls []ProviderToolCall
}

type Provider interface {
	Generate(context.Context, ProviderRequest) (ProviderResponse, error)
}

type ProviderFailure struct {
	Code   string
	Detail string
	Cause  error
}

func (failure *ProviderFailure) Error() string {
	if failure == nil {
		return "<nil>"
	}
	return fmt.Sprintf("agent provider %s: %v", failure.Code, failure.Cause)
}

func (failure *ProviderFailure) Unwrap() error {
	if failure == nil {
		return nil
	}
	return failure.Cause
}

type ToolRequest struct {
	RunID           string
	StudentNumber   string
	Analytics       *AnalyticsSnapshot
	Key             string
	Name            string
	ArgumentsSchema string
	Arguments       json.RawMessage
}

type ToolExecution struct {
	Outcome      ToolOutcome
	ResultSchema *string
	Result       json.RawMessage
	ErrorCode    *string
}

type ToolExecutor interface {
	// Execute treats RunID and Key together as the durable idempotency identity.
	// A reclaimed attempt can submit the same call after an ambiguous process
	// boundary, so side-effecting implementations must return the same outcome.
	Execute(context.Context, ToolRequest) (ToolExecution, error)
}

type WorkerRepository interface {
	Claim(context.Context, string, string, time.Duration) (*Claim, error)
	RenewLease(context.Context, Claim, time.Duration) error
	LoadWork(context.Context, Claim, int) (Work, error)
	RecordToolCall(context.Context, Claim, ToolCallRecord) (ToolCallRecord, error)
	Complete(context.Context, Claim, Completion) error
	Fail(context.Context, Claim, string, string) error
}

type DeterministicProviderStep struct {
	Response ProviderResponse
	Error    error
}

// DeterministicProvider is a race-safe scripted provider for integration and
// contract tests. Each call consumes exactly one configured step.
type DeterministicProvider struct {
	mu       sync.Mutex
	steps    []DeterministicProviderStep
	requests []ProviderRequest
}

func NewDeterministicProvider(steps []DeterministicProviderStep) (*DeterministicProvider, error) {
	if len(steps) == 0 {
		return nil, errors.New("at least one deterministic provider step is required")
	}
	copySteps := make([]DeterministicProviderStep, len(steps))
	for index, step := range steps {
		copySteps[index] = DeterministicProviderStep{Response: cloneProviderResponse(step.Response), Error: step.Error}
	}
	return &DeterministicProvider{steps: copySteps}, nil
}

func (provider *DeterministicProvider) Generate(_ context.Context, request ProviderRequest) (ProviderResponse, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.requests = append(provider.requests, cloneProviderRequest(request))
	if len(provider.steps) == 0 {
		return ProviderResponse{}, errors.New("deterministic provider script is exhausted")
	}
	step := provider.steps[0]
	provider.steps = provider.steps[1:]
	return cloneProviderResponse(step.Response), step.Error
}

func (provider *DeterministicProvider) Requests() []ProviderRequest {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	result := make([]ProviderRequest, len(provider.requests))
	for index, request := range provider.requests {
		result[index] = cloneProviderRequest(request)
	}
	return result
}

type DeterministicToolExecutor struct {
	mu         sync.Mutex
	executions map[string]ToolExecution
	requests   []ToolRequest
}

func NewDeterministicToolExecutor(executions map[string]ToolExecution) (*DeterministicToolExecutor, error) {
	if len(executions) == 0 {
		return nil, errors.New("at least one deterministic tool execution is required")
	}
	owned := make(map[string]ToolExecution, len(executions))
	for name, execution := range executions {
		if !identifierPattern.MatchString(name) {
			return nil, fmt.Errorf("tool name %q is invalid", name)
		}
		owned[name] = cloneToolExecution(execution)
	}
	return &DeterministicToolExecutor{executions: owned}, nil
}

func (executor *DeterministicToolExecutor) Execute(_ context.Context, request ToolRequest) (ToolExecution, error) {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	executor.requests = append(executor.requests, cloneToolRequest(request))
	execution, found := executor.executions[request.Name]
	if !found {
		return ToolExecution{}, fmt.Errorf("deterministic tool %q is not configured", request.Name)
	}
	return cloneToolExecution(execution), nil
}

func (executor *DeterministicToolExecutor) Requests() []ToolRequest {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	result := make([]ToolRequest, len(executor.requests))
	for index, request := range executor.requests {
		result[index] = cloneToolRequest(request)
	}
	return result
}

func cloneJSON(value json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), value...)
}

func cloneProviderRequest(request ProviderRequest) ProviderRequest {
	request.Prompt = cloneConfigurationSnapshot(request.Prompt)
	request.Model = cloneConfigurationSnapshot(request.Model)
	if request.Analytics != nil {
		analytics := *request.Analytics
		request.Analytics = &analytics
	}
	request.Conversation = append([]Message(nil), request.Conversation...)
	for index := range request.Conversation {
		request.Conversation[index] = cloneMessage(request.Conversation[index])
	}
	request.ToolCalls = append([]ToolCallRecord(nil), request.ToolCalls...)
	for index := range request.ToolCalls {
		request.ToolCalls[index] = cloneToolCallRecord(request.ToolCalls[index])
	}
	return request
}

func cloneProviderResponse(response ProviderResponse) ProviderResponse {
	if response.Assistant != nil {
		assistant := *response.Assistant
		assistant.ReasoningContent = cloneOptionalString(assistant.ReasoningContent)
		assistant.ContextSummary = cloneOptionalString(assistant.ContextSummary)
		response.Assistant = &assistant
	}
	response.ToolCalls = append([]ProviderToolCall(nil), response.ToolCalls...)
	for index := range response.ToolCalls {
		response.ToolCalls[index].Arguments = cloneJSON(response.ToolCalls[index].Arguments)
	}
	return response
}

func cloneToolRequest(request ToolRequest) ToolRequest {
	request.Arguments = cloneJSON(request.Arguments)
	if request.Analytics != nil {
		analytics := *request.Analytics
		request.Analytics = &analytics
	}
	return request
}

func cloneToolExecution(execution ToolExecution) ToolExecution {
	execution.Result = cloneJSON(execution.Result)
	execution.ResultSchema = cloneOptionalString(execution.ResultSchema)
	execution.ErrorCode = cloneOptionalString(execution.ErrorCode)
	return execution
}

func cloneConfigurationSnapshot(snapshot ConfigurationSnapshot) ConfigurationSnapshot {
	snapshot.Document = cloneJSON(snapshot.Document)
	snapshot.CredentialRef = cloneOptionalString(snapshot.CredentialRef)
	return snapshot
}

func cloneMessage(message Message) Message {
	message.ReasoningContent = cloneOptionalString(message.ReasoningContent)
	message.ContextSummary = cloneOptionalString(message.ContextSummary)
	message.RunID = cloneOptionalString(message.RunID)
	return message
}

func cloneToolCallRecord(record ToolCallRecord) ToolCallRecord {
	record.Arguments = cloneJSON(record.Arguments)
	record.Result = cloneJSON(record.Result)
	record.ResultSchema = cloneOptionalString(record.ResultSchema)
	record.ResultSHA256 = cloneOptionalString(record.ResultSHA256)
	record.ErrorCode = cloneOptionalString(record.ErrorCode)
	return record
}

func cloneOptionalString(value *string) *string {
	if value == nil {
		return nil
	}
	owned := *value
	return &owned
}
