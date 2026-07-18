package chatagent

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
	credentialdomain "github.com/kkkzbh/AscendAny/backend/internal/credential"
)

const (
	OpenAICompatibleModelSchema = "ascendany.model_connection.openai_compatible.v1"
	ChatPromptSchema            = "ascendany.prompt.chat.v1"

	minimumProviderTimeoutMilliseconds = int64(100)
	maximumProviderTimeoutMilliseconds = int64(120_000)
	maximumProviderEndpointBytes       = 2048
	maximumProviderModelBytes          = 256
	maximumProviderRequestBytes        = 2 << 20
	maximumProviderResponseBytes       = 4 << 20
	maximumProviderResponseHeaderBytes = 32 << 10
	maximumCompletionTokens            = int64(65_536)
	maximumOpenAIToolWireNameBytes     = 64
)

var openAIToolWireNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

var openAIToolWireNames = map[string]string{
	ToolAnalyticsGetSelf:     "analytics_get_self",
	ToolAgentNotesListActive: "agent_notes_list_active",
	ToolAgentNotesGetActive:  "agent_notes_get_active",
	ToolUpdateNotes:          "update_notes",
}

var runtimeToolNamesByOpenAIWireName = buildRuntimeToolNamesByOpenAIWireName()

func buildRuntimeToolNamesByOpenAIWireName() map[string]string {
	if len(openAIToolWireNames) != len(runtimeToolDefinitions) {
		panic("OpenAI tool wire-name catalog differs from the runtime tool catalog")
	}
	reverse := make(map[string]string, len(openAIToolWireNames))
	for runtimeName, wireName := range openAIToolWireNames {
		if _, supported := runtimeToolDefinitions[runtimeName]; !supported || len(wireName) > maximumOpenAIToolWireNameBytes || !openAIToolWireNamePattern.MatchString(wireName) {
			panic("OpenAI tool wire-name catalog contains an invalid entry")
		}
		if _, duplicate := reverse[wireName]; duplicate {
			panic("OpenAI tool wire-name catalog contains a duplicate wire name")
		}
		reverse[wireName] = runtimeName
	}
	return reverse
}

type modelConnectionConfiguration struct {
	Endpoint            string
	Authority           string
	Model               string
	TimeoutMilliseconds int64
	MaxCompletionTokens int64
}

type chatPromptConfiguration struct {
	SystemPrompt string
	EnabledTools []string
}

type OpenAICompatibleProvider struct {
	credentials credentialdomain.Resolver
	client      *http.Client
}

type ModelConnectionProbeResult struct {
	Authority           string
	Model               string
	LatencyMilliseconds int64
}

func NewOpenAICompatibleProvider(credentials credentialdomain.Resolver) (*OpenAICompatibleProvider, error) {
	return newOpenAICompatibleProvider(credentials, defaultOpenAITransport())
}

func newOpenAICompatibleProvider(credentials credentialdomain.Resolver, transport http.RoundTripper) (*OpenAICompatibleProvider, error) {
	if credentials == nil || transport == nil {
		return nil, domainError(ErrorInvalidConfiguration, true, "construct OpenAI-compatible provider", errors.New("credential resolver and HTTP transport are required"))
	}
	return &OpenAICompatibleProvider{
		credentials: credentials,
		client: &http.Client{
			Transport:     transport,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

func defaultOpenAITransport() *http.Transport {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	return &http.Transport{
		DialContext:            dialer.DialContext,
		ForceAttemptHTTP2:      true,
		MaxIdleConns:           100,
		MaxIdleConnsPerHost:    4,
		IdleConnTimeout:        90 * time.Second,
		TLSHandshakeTimeout:    10 * time.Second,
		ExpectContinueTimeout:  time.Second,
		ResponseHeaderTimeout:  time.Duration(maximumProviderTimeoutMilliseconds) * time.Millisecond,
		MaxResponseHeaderBytes: maximumProviderResponseHeaderBytes,
		DisableCompression:     true,
		TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12},
	}
}

func (provider *OpenAICompatibleProvider) Generate(ctx context.Context, request ProviderRequest) (ProviderResponse, error) {
	if ctx == nil {
		return ProviderResponse{}, providerError("provider_request_invalid", "provider request is invalid", errors.New("context is required"))
	}
	if err := ctx.Err(); err != nil {
		return ProviderResponse{}, err
	}
	model, failure := parseModelConnection(request.Model)
	if failure != nil {
		return ProviderResponse{}, failure
	}
	prompt, failure := parseChatPrompt(request.Prompt)
	if failure != nil {
		return ProviderResponse{}, failure
	}
	if !canonicalUUIDv4.MatchString(request.RunID) || !canonicalUUIDv4.MatchString(request.ThreadID) ||
		!canonicalUUIDv4.MatchString(request.InputMessageID) ||
		request.Kind != RunReply && request.Kind != RunAutoAnalysis || strings.TrimSpace(request.StudentNumber) != request.StudentNumber ||
		request.StudentNumber == "" || len(request.StudentNumber) > auth.MaxStudentNumberBytes || !utf8.ValidString(request.StudentNumber) {
		return ProviderResponse{}, providerError("provider_request_invalid", "provider request is invalid", errors.New("run identity is invalid"))
	}
	if err := validateRunExecutionContext(request.Kind, request.Analytics, request.AutoAnalysisContext); err != nil {
		return ProviderResponse{}, providerError("provider_request_invalid", "provider request is invalid", err)
	}

	body, failure := encodeOpenAIRequest(request, model, prompt)
	if failure != nil {
		return ProviderResponse{}, failure
	}
	runID := request.RunID
	responseBody, err := provider.executeRequest(ctx, request.Model, model, body, "AscendAny-Agent-Runtime/2", &runID)
	if err != nil {
		return ProviderResponse{}, err
	}
	return decodeOpenAIResponse(responseBody, prompt.EnabledTools)
}

func (provider *OpenAICompatibleProvider) ProbeModelConnection(
	ctx context.Context,
	snapshot ConfigurationSnapshot,
) (ModelConnectionProbeResult, error) {
	if ctx == nil {
		return ModelConnectionProbeResult{}, providerError("provider_request_invalid", "provider request is invalid", errors.New("context is required"))
	}
	if err := ctx.Err(); err != nil {
		return ModelConnectionProbeResult{}, err
	}
	model, failure := parseModelConnection(snapshot)
	if failure != nil {
		return ModelConnectionProbeResult{}, failure
	}
	maximumTokens := min(model.MaxCompletionTokens, int64(16))
	body, err := json.Marshal(openAIProbeRequest{
		Model: model.Model,
		Messages: []openAIProbeMessage{
			{Role: "system", Content: "Return exactly OK."},
			{Role: "user", Content: "Connection probe."},
		},
		MaxCompletionTokens: maximumTokens,
		Stream:              false,
	})
	if err != nil || len(body) == 0 || len(body) > maximumProviderRequestBytes {
		return ModelConnectionProbeResult{}, providerError("provider_request_invalid", "provider request is invalid", errors.New("probe request encoding failed"))
	}
	started := time.Now()
	responseBody, err := provider.executeRequest(ctx, snapshot, model, body, "AscendAny-Model-Probe/2", nil)
	if err != nil {
		return ModelConnectionProbeResult{}, err
	}
	if err := validateOpenAIProbeResponse(responseBody); err != nil {
		return ModelConnectionProbeResult{}, err
	}
	latency := time.Since(started).Milliseconds()
	if latency < 0 {
		latency = 0
	}
	return ModelConnectionProbeResult{
		Authority:           model.Authority,
		Model:               model.Model,
		LatencyMilliseconds: latency,
	}, nil
}

func (provider *OpenAICompatibleProvider) executeRequest(
	ctx context.Context,
	snapshot ConfigurationSnapshot,
	model modelConnectionConfiguration,
	body []byte,
	userAgent string,
	runID *string,
) ([]byte, error) {
	credentialValue, err := provider.credentials.Resolve(ctx, *snapshot.CredentialRef, model.Authority)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return nil, contextErr
		}
		return nil, providerError("provider_credential_unavailable", "model credential is unavailable", errors.New("credential resolution failed"))
	}
	if !credentialdomain.ValidBearer([]byte(credentialValue)) {
		return nil, providerError("provider_credential_invalid", "model credential is invalid", errors.New("resolved credential violates the bearer contract"))
	}

	requestContext, cancel := context.WithTimeout(ctx, time.Duration(model.TimeoutMilliseconds)*time.Millisecond)
	defer cancel()
	httpRequest, err := http.NewRequestWithContext(requestContext, http.MethodPost, model.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, providerError("provider_configuration_invalid", "model connection is invalid", errors.New("endpoint cannot form a request"))
	}
	httpRequest.Header.Set("Accept", "application/json")
	httpRequest.Header.Set("Authorization", "Bearer "+credentialValue)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("User-Agent", userAgent)
	if runID != nil {
		httpRequest.Header.Set("X-AscendAny-Run-ID", *runID)
	}

	response, err := provider.client.Do(httpRequest)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return nil, contextErr
		}
		if requestContext.Err() != nil {
			return nil, providerError("provider_timeout", "model request timed out", errors.New("configured timeout exceeded"))
		}
		return nil, classifyProviderTransportFailure(err)
	}
	defer response.Body.Close()

	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, maximumProviderResponseBytes+1))
	if readErr != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return nil, contextErr
		}
		if requestContext.Err() != nil {
			return nil, providerError("provider_timeout", "model response timed out", errors.New("configured timeout exceeded"))
		}
		return nil, providerError("provider_response_read_failure", "model response could not be read", errors.New("response read failed"))
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, classifyProviderStatus(response.StatusCode)
	}
	mediaType, _, mediaTypeErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if mediaTypeErr != nil || mediaType != "application/json" {
		return nil, providerError("provider_response_invalid", "model response violated the protocol", errors.New("successful response content type is not application/json"))
	}
	if len(responseBody) > maximumProviderResponseBytes {
		return nil, providerError("provider_response_too_large", "model response exceeded the size limit", errors.New("response body is oversized"))
	}
	return responseBody, nil
}

type openAIProbeRequest struct {
	Model               string               `json:"model"`
	Messages            []openAIProbeMessage `json:"messages"`
	MaxCompletionTokens int64                `json:"max_completion_tokens"`
	Stream              bool                 `json:"stream"`
}

type openAIProbeMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIProbeResponse struct {
	Choices []struct {
		Index        int64   `json:"index"`
		FinishReason *string `json:"finish_reason"`
		Message      struct {
			Role string `json:"role"`
		} `json:"message"`
	} `json:"choices"`
}

func validateOpenAIProbeResponse(body []byte) error {
	canonical, _, err := canonicaljson.Object(body, maximumProviderResponseBytes)
	if err != nil {
		return providerError("provider_response_invalid", "model response violated the protocol", errors.New("probe response is not an unambiguous JSON object"))
	}
	var response openAIProbeResponse
	if err := json.Unmarshal(canonical, &response); err != nil || len(response.Choices) != 1 ||
		response.Choices[0].Index != 0 || response.Choices[0].FinishReason == nil ||
		response.Choices[0].Message.Role != "assistant" {
		return providerError("provider_response_invalid", "model response violated the protocol", errors.New("probe response choice is invalid"))
	}
	return nil
}

type rawModelConnection struct {
	Endpoint            *string `json:"endpoint"`
	Model               *string `json:"model"`
	TimeoutMilliseconds *int64  `json:"timeoutMilliseconds"`
	MaxCompletionTokens *int64  `json:"maxCompletionTokens"`
}

func parseModelConnection(snapshot ConfigurationSnapshot) (modelConnectionConfiguration, *ProviderFailure) {
	if snapshot.SchemaID != OpenAICompatibleModelSchema || snapshot.CredentialRef == nil || !configurationKey.MatchString(*snapshot.CredentialRef) {
		return modelConnectionConfiguration{}, providerError("provider_configuration_invalid", "model connection is invalid", errors.New("schema or credential reference is invalid"))
	}
	canonical, _, err := canonicaljson.Object(snapshot.Document, MaxConfigurationBytes)
	if err != nil {
		return modelConnectionConfiguration{}, providerError("provider_configuration_invalid", "model connection is invalid", errors.New("document is invalid"))
	}
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	var raw rawModelConnection
	if err := decoder.Decode(&raw); err != nil || raw.Endpoint == nil || raw.Model == nil || raw.TimeoutMilliseconds == nil || raw.MaxCompletionTokens == nil {
		return modelConnectionConfiguration{}, providerError("provider_configuration_invalid", "model connection is invalid", errors.New("document shape is invalid"))
	}
	configuration := modelConnectionConfiguration{
		Endpoint: *raw.Endpoint, Model: *raw.Model, TimeoutMilliseconds: *raw.TimeoutMilliseconds,
		MaxCompletionTokens: *raw.MaxCompletionTokens,
	}
	if len(configuration.Endpoint) == 0 || len(configuration.Endpoint) > maximumProviderEndpointBytes || configuration.Endpoint != strings.TrimSpace(configuration.Endpoint) ||
		len(configuration.Model) == 0 || len(configuration.Model) > maximumProviderModelBytes || configuration.Model != strings.TrimSpace(configuration.Model) || !utf8.ValidString(configuration.Model) || containsControl(configuration.Model) ||
		configuration.TimeoutMilliseconds < minimumProviderTimeoutMilliseconds || configuration.TimeoutMilliseconds > maximumProviderTimeoutMilliseconds ||
		configuration.MaxCompletionTokens < 1 || configuration.MaxCompletionTokens > maximumCompletionTokens {
		return modelConnectionConfiguration{}, providerError("provider_configuration_invalid", "model connection is invalid", errors.New("model connection violates its bounds"))
	}
	parsed, err := url.Parse(configuration.Endpoint)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Hostname() == "" || parsed.Opaque != "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.RawFragment != "" || parsed.RawPath != "" ||
		parsed.Path == "" || parsed.Path[0] != '/' || path.Clean(parsed.Path) != parsed.Path || strings.Contains(parsed.Path, "//") {
		return modelConnectionConfiguration{}, providerError("provider_configuration_invalid", "model connection is invalid", errors.New("endpoint must be one canonical absolute HTTPS URL"))
	}
	host := canonicalProviderHost(parsed.Hostname())
	if host == "" || strings.HasSuffix(parsed.Host, ":") {
		return modelConnectionConfiguration{}, providerError("provider_configuration_invalid", "model connection is invalid", errors.New("endpoint host or port is invalid"))
	}
	rawPort := parsed.Port()
	if rawPort == "" {
		rawPort = "443"
	}
	port, err := strconv.ParseUint(rawPort, 10, 16)
	if err != nil || port == 0 || strconv.FormatUint(port, 10) != rawPort {
		return modelConnectionConfiguration{}, providerError("provider_configuration_invalid", "model connection is invalid", errors.New("endpoint port is invalid"))
	}
	configuration.Authority = net.JoinHostPort(host, rawPort)
	if !credentialdomain.ValidAuthority(configuration.Authority) {
		return modelConnectionConfiguration{}, providerError("provider_configuration_invalid", "model connection is invalid", errors.New("endpoint authority is invalid"))
	}
	parsed.Host = configuration.Authority
	configuration.Endpoint = parsed.String()
	return configuration, nil
}

func canonicalProviderHost(raw string) string {
	if ip := net.ParseIP(raw); ip != nil {
		return ip.String()
	}
	return strings.TrimSuffix(strings.ToLower(raw), ".")
}

type rawChatPrompt struct {
	SystemPrompt *string   `json:"systemPrompt"`
	EnabledTools *[]string `json:"enabledTools"`
}

func parseChatPrompt(snapshot ConfigurationSnapshot) (chatPromptConfiguration, *ProviderFailure) {
	if snapshot.SchemaID != ChatPromptSchema || snapshot.CredentialRef != nil {
		return chatPromptConfiguration{}, providerError("provider_prompt_invalid", "agent prompt is invalid", errors.New("schema or credential boundary is invalid"))
	}
	canonical, _, err := canonicaljson.Object(snapshot.Document, MaxConfigurationBytes)
	if err != nil {
		return chatPromptConfiguration{}, providerError("provider_prompt_invalid", "agent prompt is invalid", errors.New("document is invalid"))
	}
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	var raw rawChatPrompt
	if err := decoder.Decode(&raw); err != nil || raw.SystemPrompt == nil || raw.EnabledTools == nil {
		return chatPromptConfiguration{}, providerError("provider_prompt_invalid", "agent prompt is invalid", errors.New("document shape is invalid"))
	}
	if len(*raw.SystemPrompt) == 0 || len(*raw.SystemPrompt) > MaxMessageBytes || !utf8.ValidString(*raw.SystemPrompt) || strings.TrimSpace(*raw.SystemPrompt) == "" || len(*raw.EnabledTools) > len(runtimeToolDefinitions) {
		return chatPromptConfiguration{}, providerError("provider_prompt_invalid", "agent prompt is invalid", errors.New("prompt violates its bounds"))
	}
	seen := make(map[string]struct{}, len(*raw.EnabledTools))
	for _, name := range *raw.EnabledTools {
		if _, supported := runtimeToolDefinitions[name]; !supported {
			return chatPromptConfiguration{}, providerError("provider_prompt_invalid", "agent prompt is invalid", errors.New("prompt enables an unsupported tool"))
		}
		if _, duplicate := seen[name]; duplicate {
			return chatPromptConfiguration{}, providerError("provider_prompt_invalid", "agent prompt is invalid", errors.New("prompt repeats a tool"))
		}
		seen[name] = struct{}{}
	}
	return chatPromptConfiguration{SystemPrompt: *raw.SystemPrompt, EnabledTools: append([]string(nil), (*raw.EnabledTools)...)}, nil
}

type openAIRequest struct {
	Model               string                 `json:"model"`
	Messages            []openAIRequestMessage `json:"messages"`
	Tools               []openAITool           `json:"tools,omitempty"`
	ToolChoice          string                 `json:"tool_choice,omitempty"`
	MaxCompletionTokens int64                  `json:"max_completion_tokens"`
	Stream              bool                   `json:"stream"`
}

type openAIRequestMessage struct {
	Role       string                  `json:"role"`
	Content    *string                 `json:"content,omitempty"`
	ToolCalls  []openAIRequestToolCall `json:"tool_calls,omitempty"`
	ToolCallID string                  `json:"tool_call_id,omitempty"`
}

type openAIRequestToolCall struct {
	ID       string                    `json:"id"`
	Type     string                    `json:"type"`
	Function openAIRequestToolFunction `json:"function"`
}

type openAIRequestToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openAITool struct {
	Type     string             `json:"type"`
	Function openAIToolFunction `json:"function"`
}

type openAIToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

func encodeOpenAIRequest(request ProviderRequest, model modelConnectionConfiguration, prompt chatPromptConfiguration) ([]byte, *ProviderFailure) {
	messages := make([]openAIRequestMessage, 0, 1+len(request.Conversation)+2*len(request.ToolCalls))
	messages = append(messages, openAIRequestMessage{Role: "system", Content: runtimeStringPointer(prompt.SystemPrompt)})
	enabledTools := make(map[string]struct{}, len(prompt.EnabledTools))
	for _, name := range prompt.EnabledTools {
		enabledTools[name] = struct{}{}
	}
	inputMessageFound := false
	for _, message := range request.Conversation {
		if err := validateMessage(message); err != nil || message.ThreadID != request.ThreadID {
			return nil, providerError("provider_request_invalid", "provider request is invalid", errors.New("conversation is invalid"))
		}
		if message.Kind == MessageAutoAnalysisRequest {
			frontendContext, err := decodeAutoAnalysisInputContent(message.Content)
			if err != nil {
				return nil, providerError("provider_request_invalid", "provider request is invalid", errors.New("automatic analysis conversation is invalid"))
			}
			if message.ID == request.InputMessageID && (request.Kind != RunAutoAnalysis ||
				request.AutoAnalysisContext == nil || frontendContext != *request.AutoAnalysisContext) {
				return nil, providerError("provider_request_invalid", "provider request is invalid", errors.New("automatic analysis context differs from the durable input"))
			}
		}
		if message.ID == request.InputMessageID {
			if inputMessageFound || request.Kind == RunReply && message.Kind != MessageUser ||
				request.Kind == RunAutoAnalysis && message.Kind != MessageAutoAnalysisRequest {
				return nil, providerError("provider_request_invalid", "provider request is invalid", errors.New("run input message is invalid"))
			}
			inputMessageFound = true
		}
		role := "user"
		if message.Kind == MessageAssistant {
			role = "assistant"
		}
		content := message.Content
		messages = append(messages, openAIRequestMessage{Role: role, Content: &content})
	}
	if !inputMessageFound {
		return nil, providerError("provider_request_invalid", "provider request is invalid", errors.New("run input message is absent"))
	}
	for _, record := range request.ToolCalls {
		definition, exists := runtimeToolDefinitions[record.Name]
		wireName, mapped := openAIToolWireNames[record.Name]
		_, enabled := enabledTools[record.Name]
		if !exists || !mapped || !enabled || record.ArgumentsSchema != definition.ArgumentsSchema || !toolCallKeyPattern.MatchString(record.Key) ||
			record.Outcome == ToolSucceeded && (record.ResultSchema == nil || *record.ResultSchema != definition.ResultSchema) {
			return nil, providerError("provider_request_invalid", "provider request is invalid", errors.New("stored tool call is invalid"))
		}
		messages = append(messages, openAIRequestMessage{Role: "assistant", ToolCalls: []openAIRequestToolCall{{
			ID: record.Key, Type: "function", Function: openAIRequestToolFunction{Name: wireName, Arguments: string(record.Arguments)},
		}}})
		result, failure := encodeToolHistoryResult(record)
		if failure != nil {
			return nil, failure
		}
		messages = append(messages, openAIRequestMessage{Role: "tool", Content: runtimeStringPointer(result), ToolCallID: record.Key})
	}
	tools := make([]openAITool, 0, len(prompt.EnabledTools))
	for _, name := range prompt.EnabledTools {
		definition := runtimeToolDefinitions[name]
		wireName, mapped := openAIToolWireNames[name]
		if !mapped {
			return nil, providerError("provider_request_invalid", "provider request is invalid", errors.New("enabled tool has no provider wire name"))
		}
		tools = append(tools, openAITool{Type: "function", Function: openAIToolFunction{
			Name: wireName, Description: definition.Description, Parameters: definition.Parameters,
		}})
	}
	payload := openAIRequest{Model: model.Model, Messages: messages, Tools: tools, MaxCompletionTokens: model.MaxCompletionTokens}
	if len(tools) > 0 {
		payload.ToolChoice = "auto"
	}
	body, err := json.Marshal(payload)
	if err != nil || len(body) == 0 || len(body) > maximumProviderRequestBytes {
		return nil, providerError("provider_request_too_large", "provider request exceeded the size limit", errors.New("request encoding is oversized"))
	}
	return body, nil
}

func encodeToolHistoryResult(record ToolCallRecord) (string, *ProviderFailure) {
	if record.Outcome == ToolSucceeded && record.ResultSchema != nil && len(record.Result) > 0 && record.ErrorCode == nil {
		canonical, _, err := canonicaljson.Object(record.Result, MaxToolDocumentBytes)
		if err == nil && bytes.Equal(canonical, record.Result) {
			return string(record.Result), nil
		}
	}
	if (record.Outcome == ToolFailed || record.Outcome == ToolDenied) && record.ResultSchema == nil && len(record.Result) == 0 &&
		record.ErrorCode != nil && identifierPattern.MatchString(*record.ErrorCode) {
		encoded, err := json.Marshal(map[string]string{"outcome": string(record.Outcome), "errorCode": *record.ErrorCode})
		if err == nil {
			return string(encoded), nil
		}
	}
	return "", providerError("provider_request_invalid", "provider request is invalid", errors.New("stored tool result is invalid"))
}

type openAIResponse struct {
	Choices []openAIChoice `json:"choices"`
}

type openAIChoice struct {
	Index        int64                 `json:"index"`
	FinishReason *string               `json:"finish_reason"`
	Message      openAIResponseMessage `json:"message"`
}

type openAIResponseMessage struct {
	Role             string                   `json:"role"`
	Content          json.RawMessage          `json:"content"`
	ReasoningContent json.RawMessage          `json:"reasoning_content"`
	ToolCalls        []openAIResponseToolCall `json:"tool_calls"`
}

type openAIResponseToolCall struct {
	ID       string                     `json:"id"`
	Type     string                     `json:"type"`
	Function openAIResponseToolFunction `json:"function"`
}

type openAIResponseToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

func decodeOpenAIResponse(body []byte, enabledTools []string) (ProviderResponse, error) {
	canonical, _, err := canonicaljson.Object(body, maximumProviderResponseBytes)
	if err != nil {
		return ProviderResponse{}, providerError("provider_response_invalid", "model response violated the protocol", errors.New("response is not an unambiguous JSON object"))
	}
	var response openAIResponse
	if err := json.Unmarshal(canonical, &response); err != nil || len(response.Choices) != 1 || response.Choices[0].Index != 0 || response.Choices[0].FinishReason == nil || response.Choices[0].Message.Role != "assistant" {
		return ProviderResponse{}, providerError("provider_response_invalid", "model response violated the protocol", errors.New("response choice is invalid"))
	}
	choice := response.Choices[0]
	if len(choice.Message.ToolCalls) > 0 {
		_, contentValid := decodeBoundedOptionalString(choice.Message.Content, MaxMessageBytes)
		_, reasoningValid := decodeBoundedOptionalString(choice.Message.ReasoningContent, MaxReasoningBytes)
		if *choice.FinishReason != "tool_calls" || len(choice.Message.ToolCalls) > MaxProviderToolCallsPerTurn || !contentValid || !reasoningValid {
			return ProviderResponse{}, providerError("provider_response_invalid", "model response violated the protocol", errors.New("tool response metadata is invalid"))
		}
		enabled := make(map[string]struct{}, len(enabledTools))
		for _, name := range enabledTools {
			enabled[name] = struct{}{}
		}
		calls := make([]ProviderToolCall, 0, len(choice.Message.ToolCalls))
		seen := make(map[string]struct{}, len(choice.Message.ToolCalls))
		for _, call := range choice.Message.ToolCalls {
			runtimeName, mapped := runtimeToolNamesByOpenAIWireName[call.Function.Name]
			definition, supported := runtimeToolDefinitions[runtimeName]
			_, permitted := enabled[runtimeName]
			if call.Type != "function" || !toolCallKeyPattern.MatchString(call.ID) || !mapped || !supported || !permitted {
				return ProviderResponse{}, providerError("provider_response_invalid", "model response violated the protocol", errors.New("tool call is unsupported or invalid"))
			}
			if _, duplicate := seen[call.ID]; duplicate {
				return ProviderResponse{}, providerError("provider_response_invalid", "model response violated the protocol", errors.New("tool call ID is duplicated"))
			}
			seen[call.ID] = struct{}{}
			arguments, _, err := canonicaljson.Object(json.RawMessage(call.Function.Arguments), MaxToolDocumentBytes)
			if err != nil {
				return ProviderResponse{}, providerError("provider_response_invalid", "model response violated the protocol", errors.New("tool arguments are invalid"))
			}
			calls = append(calls, ProviderToolCall{Key: call.ID, Name: runtimeName, ArgumentsSchema: definition.ArgumentsSchema, Arguments: arguments})
		}
		return ProviderResponse{ToolCalls: calls}, nil
	}
	if *choice.FinishReason != "stop" {
		return ProviderResponse{}, providerError("provider_output_incomplete", "model output was incomplete", errors.New("terminal finish reason is not stop"))
	}
	content, ok := decodeBoundedOptionalString(choice.Message.Content, MaxMessageBytes)
	if !ok || content == nil {
		return ProviderResponse{}, providerError("provider_response_invalid", "model response violated the protocol", errors.New("assistant content is absent"))
	}
	reasoning, ok := decodeBoundedOptionalString(choice.Message.ReasoningContent, MaxReasoningBytes)
	if !ok {
		return ProviderResponse{}, providerError("provider_response_invalid", "model response violated the protocol", errors.New("reasoning content is invalid"))
	}
	output := AssistantOutput{Content: *content, ReasoningContent: reasoning}
	if err := validateAssistantOutput(output); err != nil {
		return ProviderResponse{}, providerError("provider_response_invalid", "model response violated the protocol", errors.New("assistant output violates its bounds"))
	}
	return ProviderResponse{Assistant: &output}, nil
}

func decodeBoundedOptionalString(raw json.RawMessage, maximumBytes int) (*string, bool) {
	if maximumBytes < 0 {
		return nil, false
	}
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, true
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil || len(value) > maximumBytes || !utf8.ValidString(value) || strings.IndexByte(value, 0) >= 0 {
		return nil, false
	}
	return &value, true
}

func providerError(code, detail string, cause error) *ProviderFailure {
	return &ProviderFailure{Code: code, Detail: detail, Cause: cause}
}

func classifyProviderStatus(status int) *ProviderFailure {
	switch {
	case status >= 300 && status < 400:
		return providerError("provider_redirect_rejected", "model endpoint redirected the request", fmt.Errorf("status %d", status))
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return providerError("provider_auth_rejected", "model endpoint rejected authorization", fmt.Errorf("status %d", status))
	case status == http.StatusRequestTimeout || status == http.StatusTooEarly || status == http.StatusTooManyRequests || status >= 500 && status <= 599:
		return providerError("provider_temporarily_unavailable", "model endpoint is temporarily unavailable", fmt.Errorf("status %d", status))
	case status >= 400 && status < 500:
		return providerError("provider_request_rejected", "model endpoint rejected the request", fmt.Errorf("status %d", status))
	default:
		return providerError("provider_protocol_rejected", "model endpoint returned an unsupported status", fmt.Errorf("status %d", status))
	}
}

func classifyProviderTransportFailure(err error) *ProviderFailure {
	var certificateInvalid x509.CertificateInvalidError
	var hostnameInvalid x509.HostnameError
	var unknownAuthority x509.UnknownAuthorityError
	if errors.As(err, &certificateInvalid) || errors.As(err, &hostnameInvalid) || errors.As(err, &unknownAuthority) {
		return providerError("provider_tls_rejected", "model TLS verification failed", errors.New("TLS certificate rejected"))
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return providerError("provider_timeout", "model request timed out", errors.New("network timeout"))
	}
	return providerError("provider_transport_failure", "model endpoint could not be reached", errors.New("transport failed"))
}

func runtimeStringPointer(value string) *string { return &value }
