// Package traineragentprotocol defines the capability-free authenticated HTTP
// value contract shared by the online server and the remote trainer agent.
package traineragentprotocol

import "encoding/json"

const (
	ClaimRequestProtocolV1      = "ascendany.recommendation.trainer-agent.claim-request.v1"
	ClaimResponseProtocolV1     = "ascendany.recommendation.trainer-agent.claim-response.v1"
	HeartbeatRequestProtocolV1  = "ascendany.recommendation.trainer-agent.heartbeat-request.v1"
	HeartbeatResponseProtocolV1 = "ascendany.recommendation.trainer-agent.heartbeat-response.v1"
	OutputRequestProtocolV1     = "ascendany.recommendation.trainer-agent.output-request.v1"
	OutputResponseProtocolV1    = "ascendany.recommendation.trainer-agent.output-response.v1"
	FailureRequestProtocolV1    = "ascendany.recommendation.trainer-agent.failure-request.v1"
	FailureResponseProtocolV1   = "ascendany.recommendation.trainer-agent.failure-response.v1"
	ErrorResponseProtocolV1     = "ascendany.recommendation.trainer-agent.error-response.v1"

	ClaimMediaTypeV1     = "application/vnd.ascendany.recommendation.trainer-agent.claim.v1+json"
	HeartbeatMediaTypeV1 = "application/vnd.ascendany.recommendation.trainer-agent.heartbeat.v1+json"
	OutputMediaTypeV1    = "application/vnd.ascendany.recommendation.trainer-agent.output.v1+json"
	FailureMediaTypeV1   = "application/vnd.ascendany.recommendation.trainer-agent.failure.v1+json"
	ErrorMediaTypeV1     = "application/vnd.ascendany.recommendation.trainer-agent.error.v1+json"

	HTTPBasePathV1 = "/api/v2/internal/recommendation/trainer-agent"

	PublicationActivated  = "activated"
	PublicationSuperseded = "superseded"
	FailureRecorded       = "failed"
	FailureRequeued       = "requeued"
)

type ClaimRequestV1 struct {
	Protocol                  string `json:"protocol"`
	AgentID                   string `json:"agentId"`
	LeaseDurationMilliseconds int64  `json:"leaseDurationMilliseconds"`
}

type ClaimResponseV1 struct {
	Protocol                  string          `json:"protocol"`
	RunID                     string          `json:"runId"`
	AttemptToken              string          `json:"attemptToken"`
	LeaseDurationMilliseconds int64           `json:"leaseDurationMilliseconds"`
	LeaseExpiresAt            string          `json:"leaseExpiresAt"`
	InputManifestSHA256       string          `json:"inputManifestSha256"`
	InputBundleSHA256         string          `json:"inputBundleSha256"`
	InputBundle               json.RawMessage `json:"inputBundle"`
}

type HeartbeatRequestV1 struct {
	Protocol     string `json:"protocol"`
	AgentID      string `json:"agentId"`
	AttemptToken string `json:"attemptToken"`
}

type HeartbeatResponseV1 struct {
	Protocol       string `json:"protocol"`
	LeaseExpiresAt string `json:"leaseExpiresAt"`
}

type OutputRequestV1 struct {
	Protocol            string          `json:"protocol"`
	AgentID             string          `json:"agentId"`
	AttemptToken        string          `json:"attemptToken"`
	InputManifestSHA256 string          `json:"inputManifestSha256"`
	OutputBundleSHA256  string          `json:"outputBundleSha256"`
	OutputBundle        json.RawMessage `json:"outputBundle"`
}

type OutputResponseV1 struct {
	Protocol                  string `json:"protocol"`
	Disposition               string `json:"disposition"`
	ModelID                   string `json:"modelId"`
	RuntimeConstructionSHA256 string `json:"runtimeConstructionSha256"`
	RuntimeProvenanceSHA256   string `json:"runtimeProvenanceSha256"`
	RuntimeTreeSHA256         string `json:"runtimeTreeSha256"`
	HostCapabilitySHA256      string `json:"hostCapabilitySha256"`
	RuntimeAttestationSHA256  string `json:"runtimeAttestationSha256"`
}

type FailureRequestV1 struct {
	Protocol     string `json:"protocol"`
	AgentID      string `json:"agentId"`
	AttemptToken string `json:"attemptToken"`
	Code         string `json:"code"`
	Detail       string `json:"detail"`
	Retryable    bool   `json:"retryable"`
}

type FailureResponseV1 struct {
	Protocol    string `json:"protocol"`
	Disposition string `json:"disposition"`
}

type ErrorResponseV1 struct {
	Protocol  string `json:"protocol"`
	Code      string `json:"code"`
	Detail    string `json:"detail"`
	Retryable bool   `json:"retryable"`
}
