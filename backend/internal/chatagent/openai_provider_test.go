package chatagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type providerRoundTripFunc func(*http.Request) (*http.Response, error)

func (function providerRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

type providerCredentialResolver struct {
	secret    string
	err       error
	reference string
	authority string
	calls     int
}

func (resolver *providerCredentialResolver) Resolve(_ context.Context, reference, authority string) (string, error) {
	resolver.calls++
	resolver.reference = reference
	resolver.authority = authority
	return resolver.secret, resolver.err
}

func TestOpenAICompatibleProviderSendsBoundedHTTPSRequestAndReturnsAssistant(t *testing.T) {
	t.Parallel()
	credentials := &providerCredentialResolver{secret: "sk-exact_secret"}
	transportCalled := false
	provider := mustOpenAIProvider(t, credentials, providerRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		transportCalled = true
		if request.URL.String() != "https://models.example.test:443/v1/chat/completions" || request.Method != http.MethodPost {
			t.Fatalf("request=%s %s", request.Method, request.URL)
		}
		if request.Header.Get("Authorization") != "Bearer sk-exact_secret" || request.Header.Get("X-AscendAny-Run-ID") != testRunID {
			t.Fatalf("headers=%v", request.Header)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["model"] != "reasoner-v1" || payload["tool_choice"] != "auto" || payload["stream"] != false || len(payload["messages"].([]any)) != 2 || len(payload["tools"].([]any)) != 3 {
			t.Fatalf("payload=%s", body)
		}
		if strings.Contains(string(body), "20260001") {
			t.Fatalf("student identity leaked into model body: %s", body)
		}
		return providerHTTPResponse(200, `{"id":"chatcmpl-1","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"Clear answer","reasoning_content":"bounded reasoning","tool_calls":null}}]}`), nil
	}))

	response, err := provider.Generate(context.Background(), validProviderRequest())
	if err != nil {
		t.Fatal(err)
	}
	if !transportCalled || credentials.calls != 1 || credentials.reference != "models.primary" || credentials.authority != "models.example.test:443" {
		t.Fatalf("credentials=%#v transport=%t", credentials, transportCalled)
	}
	if response.Assistant == nil || response.Assistant.Content != "Clear answer" || response.Assistant.ReasoningContent == nil || *response.Assistant.ReasoningContent != "bounded reasoning" || len(response.ToolCalls) != 0 {
		t.Fatalf("response=%#v", response)
	}
}

func TestOpenAICompatibleProviderProbesExactModelWithoutReturningCredential(t *testing.T) {
	t.Parallel()
	credentials := &providerCredentialResolver{secret: "probe-secret"}
	provider := mustOpenAIProvider(t, credentials, providerRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "https://models.example.test:443/v1/chat/completions" || request.Method != http.MethodPost {
			t.Fatalf("request=%s %s", request.Method, request.URL)
		}
		if request.Header.Get("Authorization") != "Bearer probe-secret" ||
			request.Header.Get("User-Agent") != "AscendAny-Model-Probe/2" ||
			request.Header.Get("X-AscendAny-Run-ID") != "" {
			t.Fatalf("headers=%v", request.Header)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		var payload struct {
			Model               string `json:"model"`
			MaxCompletionTokens int64  `json:"max_completion_tokens"`
			Stream              bool   `json:"stream"`
			Messages            []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Model != "reasoner-v1" || payload.MaxCompletionTokens != 16 || payload.Stream ||
			len(payload.Messages) != 2 || payload.Messages[0].Role != "system" || payload.Messages[1].Role != "user" {
			t.Fatalf("payload=%s", body)
		}
		return providerHTTPResponse(200, `{"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"OK"}}]}`), nil
	}))

	result, err := provider.ProbeModelConnection(context.Background(), validProviderRequest().Model)
	if err != nil {
		t.Fatal(err)
	}
	if result.Authority != "models.example.test:443" || result.Model != "reasoner-v1" ||
		result.LatencyMilliseconds < 0 || credentials.calls != 1 ||
		credentials.reference != "models.primary" || credentials.authority != "models.example.test:443" {
		t.Fatalf("result=%#v credentials=%#v", result, credentials)
	}
	if strings.Contains(fmt.Sprintf("%#v", result), credentials.secret) {
		t.Fatalf("probe result exposed credential: %#v", result)
	}
}

func TestOpenAICompatibleProviderProbeRejectsProtocolAndSanitizesFailures(t *testing.T) {
	t.Parallel()
	secret := "never-expose-probe-secret"
	provider := mustOpenAIProvider(t, &providerCredentialResolver{secret: secret}, providerRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return providerHTTPResponse(200, `{"choices":[{"index":0,"finish_reason":null,"message":{"role":"assistant"}}]}`), nil
	}))
	_, err := provider.ProbeModelConnection(context.Background(), validProviderRequest().Model)
	var failure *ProviderFailure
	if !errors.As(err, &failure) || failure.Code != "provider_response_invalid" || strings.Contains(err.Error(), secret) {
		t.Fatalf("protocol error=%v", err)
	}

	provider = mustOpenAIProvider(t, &providerCredentialResolver{err: errors.New("vault says " + secret)}, providerRoundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("transport called")
		return nil, nil
	}))
	_, err = provider.ProbeModelConnection(context.Background(), validProviderRequest().Model)
	if !errors.As(err, &failure) || failure.Code != "provider_credential_unavailable" || strings.Contains(err.Error(), secret) {
		t.Fatalf("credential error=%v", err)
	}
}

func TestOpenAICompatibleProviderMapsEnabledToolCallsToOwnedSchemas(t *testing.T) {
	t.Parallel()
	provider := mustOpenAIProvider(t, &providerCredentialResolver{secret: "token"}, providerRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return providerHTTPResponse(200, `{"choices":[{"index":0,"finish_reason":"tool_calls","message":{"role":"assistant","content":null,"tool_calls":[{"id":"call:1","type":"function","function":{"name":"analytics.get_self","arguments":"{\"historyLimit\":10}"}}]}}]}`), nil
	}))
	response, err := provider.Generate(context.Background(), validProviderRequest())
	if err != nil {
		t.Fatal(err)
	}
	if response.Assistant != nil || len(response.ToolCalls) != 1 || response.ToolCalls[0].Key != "call:1" ||
		response.ToolCalls[0].ArgumentsSchema != AnalyticsGetSelfArgumentsSchema || string(response.ToolCalls[0].Arguments) != `{"historyLimit":10}` {
		t.Fatalf("response=%#v", response)
	}
}

func TestOpenAICompatibleProviderReplaysDurableToolRecordsAsProtocolPairs(t *testing.T) {
	t.Parallel()
	request := validProviderRequest()
	resultSchema := AnalyticsGetSelfResultSchema
	request.ToolCalls = []ToolCallRecord{{
		Key: "call:1", Name: ToolAnalyticsGetSelf, ArgumentsSchema: AnalyticsGetSelfArgumentsSchema,
		Arguments: json.RawMessage(`{"historyLimit":10}`), Outcome: ToolSucceeded,
		ResultSchema: &resultSchema, Result: json.RawMessage(`{"headRevision":3,"metrics":null,"rating":null,"state":"no_observations"}`),
	}}
	provider := mustOpenAIProvider(t, &providerCredentialResolver{secret: "token"}, providerRoundTripFunc(func(httpRequest *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(httpRequest.Body)
		var payload struct {
			Messages []struct {
				Role       string `json:"role"`
				ToolCallID string `json:"tool_call_id"`
				ToolCalls  []struct {
					ID string `json:"id"`
				} `json:"tool_calls"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.Messages) != 4 || payload.Messages[2].Role != "assistant" || payload.Messages[2].ToolCalls[0].ID != "call:1" ||
			payload.Messages[3].Role != "tool" || payload.Messages[3].ToolCallID != "call:1" {
			t.Fatalf("messages=%#v body=%s", payload.Messages, body)
		}
		return providerHTTPResponse(200, `{"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"Done"}}]}`), nil
	}))
	if _, err := provider.Generate(context.Background(), request); err != nil {
		t.Fatal(err)
	}
}

func TestOpenAICompatibleProviderRejectsInvalidConfigurationBeforeCredentialOrTransport(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*ProviderRequest){
		"http endpoint": func(request *ProviderRequest) {
			request.Model.Document = json.RawMessage(`{"endpoint":"http://models.example/v1/chat/completions","model":"m","timeoutMilliseconds":1000,"maxCompletionTokens":10}`)
		},
		"unknown model field": func(request *ProviderRequest) {
			request.Model.Document = json.RawMessage(`{"endpoint":"https://models.example/v1/chat/completions","model":"m","timeoutMilliseconds":1000,"maxCompletionTokens":10,"apiKey":"secret"}`)
		},
		"missing credential": func(request *ProviderRequest) { request.Model.CredentialRef = nil },
		"unknown prompt tool": func(request *ProviderRequest) {
			request.Prompt.Document = json.RawMessage(`{"systemPrompt":"prompt","enabledTools":["shell.execute"]}`)
		},
		"prompt credential": func(request *ProviderRequest) {
			reference := "prompts.secret"
			request.Prompt.CredentialRef = &reference
		},
	} {
		name, mutate := name, mutate
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			credentials := &providerCredentialResolver{secret: "token"}
			transportCalled := false
			provider := mustOpenAIProvider(t, credentials, providerRoundTripFunc(func(*http.Request) (*http.Response, error) {
				transportCalled = true
				return nil, errors.New("must not execute")
			}))
			request := validProviderRequest()
			mutate(&request)
			_, err := provider.Generate(context.Background(), request)
			var failure *ProviderFailure
			if !errors.As(err, &failure) || credentials.calls != 0 || transportCalled {
				t.Fatalf("error=%v credentials=%d transport=%t", err, credentials.calls, transportCalled)
			}
		})
	}
}

func TestOpenAICompatibleProviderSanitizesCredentialTransportAndHTTPFailures(t *testing.T) {
	t.Parallel()
	secret := "never-expose-this-token"
	credentialFailure := &providerCredentialResolver{err: errors.New("vault says " + secret)}
	provider := mustOpenAIProvider(t, credentialFailure, providerRoundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("transport called")
		return nil, nil
	}))
	_, err := provider.Generate(context.Background(), validProviderRequest())
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("credential error=%v", err)
	}

	provider = mustOpenAIProvider(t, &providerCredentialResolver{secret: secret}, providerRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("dial failed with " + secret)
	}))
	_, err = provider.Generate(context.Background(), validProviderRequest())
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("transport error=%v", err)
	}

	provider = mustOpenAIProvider(t, &providerCredentialResolver{secret: secret}, providerRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return providerHTTPResponse(401, `{"error":{"message":"`+secret+`"}}`), nil
	}))
	_, err = provider.Generate(context.Background(), validProviderRequest())
	var failure *ProviderFailure
	if !errors.As(err, &failure) || failure.Code != "provider_auth_rejected" || strings.Contains(err.Error(), secret) {
		t.Fatalf("HTTP error=%v", err)
	}
}

func TestOpenAICompatibleProviderRejectsAmbiguousOrIncompleteResponses(t *testing.T) {
	t.Parallel()
	responses := map[string]string{
		"duplicate key":     `{"choices":[{"index":0,"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"x"}}]}`,
		"multiple choices":  `{"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"x"}},{"index":1,"finish_reason":"stop","message":{"role":"assistant","content":"y"}}]}`,
		"length finish":     `{"choices":[{"index":0,"finish_reason":"length","message":{"role":"assistant","content":"partial"}}]}`,
		"disabled tool":     `{"choices":[{"index":0,"finish_reason":"tool_calls","message":{"role":"assistant","content":null,"tool_calls":[{"id":"call:1","type":"function","function":{"name":"unknown.tool","arguments":"{}"}}]}}]}`,
		"duplicate tool id": `{"choices":[{"index":0,"finish_reason":"tool_calls","message":{"role":"assistant","content":null,"tool_calls":[{"id":"call:1","type":"function","function":{"name":"analytics.get_self","arguments":"{\"historyLimit\":1}"}},{"id":"call:1","type":"function","function":{"name":"analytics.get_self","arguments":"{\"historyLimit\":2}"}}]}}]}`,
		"nul assistant":     `{"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"invalid\u0000content"}}]}`,
	}
	for name, body := range responses {
		name, body := name, body
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			provider := mustOpenAIProvider(t, &providerCredentialResolver{secret: "token"}, providerRoundTripFunc(func(*http.Request) (*http.Response, error) {
				return providerHTTPResponse(200, body), nil
			}))
			if _, err := provider.Generate(context.Background(), validProviderRequest()); err == nil {
				t.Fatal("response accepted")
			}
		})
	}
}

func TestOpenAICompatibleProviderEnforcesTimeoutContentTypeAndResponseLimit(t *testing.T) {
	t.Parallel()
	t.Run("timeout", func(t *testing.T) {
		t.Parallel()
		request := validProviderRequest()
		request.Model.Document = json.RawMessage(`{"endpoint":"https://models.example/v1/chat/completions","model":"m","timeoutMilliseconds":100,"maxCompletionTokens":10}`)
		provider := mustOpenAIProvider(t, &providerCredentialResolver{secret: "token"}, providerRoundTripFunc(func(httpRequest *http.Request) (*http.Response, error) {
			<-httpRequest.Context().Done()
			return nil, httpRequest.Context().Err()
		}))
		_, err := provider.Generate(context.Background(), request)
		var failure *ProviderFailure
		if !errors.As(err, &failure) || failure.Code != "provider_timeout" {
			t.Fatalf("error=%v", err)
		}
	})
	t.Run("content type", func(t *testing.T) {
		t.Parallel()
		provider := mustOpenAIProvider(t, &providerCredentialResolver{secret: "token"}, providerRoundTripFunc(func(*http.Request) (*http.Response, error) {
			response := providerHTTPResponse(200, `{"choices":[]}`)
			response.Header.Set("Content-Type", "text/plain")
			return response, nil
		}))
		_, err := provider.Generate(context.Background(), validProviderRequest())
		var failure *ProviderFailure
		if !errors.As(err, &failure) || failure.Code != "provider_response_invalid" {
			t.Fatalf("error=%v", err)
		}
	})
	t.Run("response size", func(t *testing.T) {
		t.Parallel()
		provider := mustOpenAIProvider(t, &providerCredentialResolver{secret: "token"}, providerRoundTripFunc(func(*http.Request) (*http.Response, error) {
			return providerHTTPResponse(200, strings.Repeat("x", maximumProviderResponseBytes+1)), nil
		}))
		_, err := provider.Generate(context.Background(), validProviderRequest())
		var failure *ProviderFailure
		if !errors.As(err, &failure) || failure.Code != "provider_response_too_large" {
			t.Fatalf("error=%v", err)
		}
	})
}

func TestOpenAICompatibleProviderRejectsOversizedRequestBeforeCredentialResolution(t *testing.T) {
	t.Parallel()
	request := validProviderRequest()
	request.Conversation = make([]Message, 20)
	for index := range request.Conversation {
		request.Conversation[index] = Message{
			ID: testMessageID, ThreadID: testThreadID, Sequence: int64(index + 1), Kind: MessageUser,
			Content: strings.Repeat("x", MaxMessageBytes), CreatedAt: time.Date(2026, 7, 11, 0, 0, 0, index, time.UTC),
		}
	}
	credentials := &providerCredentialResolver{secret: "token"}
	transportCalled := false
	provider := mustOpenAIProvider(t, credentials, providerRoundTripFunc(func(*http.Request) (*http.Response, error) {
		transportCalled = true
		return nil, errors.New("must not execute")
	}))
	_, err := provider.Generate(context.Background(), request)
	var failure *ProviderFailure
	if !errors.As(err, &failure) || failure.Code != "provider_request_too_large" || credentials.calls != 0 || transportCalled {
		t.Fatalf("error=%v credentials=%d transport=%t", err, credentials.calls, transportCalled)
	}
}

func TestOpenAICompatibleProviderMapsHTTPStatusesWithoutReadingErrorDetails(t *testing.T) {
	t.Parallel()
	for status, code := range map[int]string{
		302: "provider_redirect_rejected", 400: "provider_request_rejected", 403: "provider_auth_rejected",
		429: "provider_temporarily_unavailable", 503: "provider_temporarily_unavailable",
	} {
		status, code := status, code
		t.Run(code+http.StatusText(status), func(t *testing.T) {
			t.Parallel()
			provider := mustOpenAIProvider(t, &providerCredentialResolver{secret: "token"}, providerRoundTripFunc(func(*http.Request) (*http.Response, error) {
				return providerHTTPResponse(status, `{"private":"must not surface"}`), nil
			}))
			_, err := provider.Generate(context.Background(), validProviderRequest())
			var failure *ProviderFailure
			if !errors.As(err, &failure) || failure.Code != code || strings.Contains(err.Error(), "must not surface") {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func mustOpenAIProvider(t *testing.T, resolver *providerCredentialResolver, transport http.RoundTripper) *OpenAICompatibleProvider {
	t.Helper()
	provider, err := newOpenAICompatibleProvider(resolver, transport)
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func providerHTTPResponse(status int, body string) *http.Response {
	header := make(http.Header)
	header.Set("Content-Type", "application/json")
	return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body))}
}

func validProviderRequest() ProviderRequest {
	credentialReference := "models.primary"
	now := time.Date(2026, 7, 11, 1, 2, 3, 0, time.UTC)
	return ProviderRequest{
		RunID: testRunID, Kind: RunReply, ThreadID: testThreadID, StudentNumber: "20260001",
		Prompt: ConfigurationSnapshot{
			Key: "prompts.chat", SchemaID: ChatPromptSchema,
			Document: json.RawMessage(`{"systemPrompt":"You are a precise student coach.","enabledTools":["analytics.get_self","agent_notes.list_active","agent_notes.get_active"]}`),
		},
		Model: ConfigurationSnapshot{
			Key: "models.primary", SchemaID: OpenAICompatibleModelSchema, CredentialRef: &credentialReference,
			Document: json.RawMessage(`{"endpoint":"https://models.example.test/v1/chat/completions","model":"reasoner-v1","timeoutMilliseconds":30000,"maxCompletionTokens":4096}`),
		},
		Conversation: []Message{{ID: testMessageID, ThreadID: testThreadID, Sequence: 1, Kind: MessageUser, Content: "Help me improve.", CreatedAt: now}},
	}
}
