package judgeprotocol

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"testing"

	"github.com/kkkzbh/AscendAny/backend/internal/judgecontract"
)

func TestRequestRoundTripStreamsAndBindsPayloads(t *testing.T) {
	source := []byte("int main() { return 0; }")
	stdin := []byte("input\n")
	bundle := []byte("strict bundle")
	header := requestHeader(source, stdin, bundle)
	var wire bytes.Buffer
	if err := WriteRequest(&wire, header, Payloads{
		Source: bytes.NewReader(source), Stdin: bytes.NewReader(stdin), TestBundle: bytes.NewReader(bundle),
	}); err != nil {
		t.Fatal(err)
	}
	received := make(map[PayloadKind][]byte)
	decoded, err := ReadRequest(&wire, acceptHeader, func(_ RequestHeader, kind PayloadKind, _ Artifact, content io.Reader) error {
		value, readErr := io.ReadAll(content)
		received[kind] = value
		return readErr
	})
	if err != nil {
		t.Fatal(err)
	}
	if decoded.JudgeJobID != header.JudgeJobID || !bytes.Equal(received[PayloadSource], source) ||
		!bytes.Equal(received[PayloadStdin], stdin) || !bytes.Equal(received[PayloadTestBundle], bundle) {
		t.Fatalf("decoded=%#v payloads=%v", decoded, received)
	}
}

func TestRequestRejectsDigestMismatchAndUnreadPayload(t *testing.T) {
	source := []byte("source")
	bundle := []byte("bundle")
	header := requestHeader(source, nil, bundle)
	corrupt := header
	corrupt.Source.SHA256 = digest([]byte("different"))
	if err := WriteRequest(io.Discard, corrupt, Payloads{
		Source: bytes.NewReader(source), TestBundle: bytes.NewReader(bundle),
	}); err == nil {
		t.Fatal("WriteRequest() error = nil")
	}

	var wire bytes.Buffer
	if err := WriteRequest(&wire, header, Payloads{Source: bytes.NewReader(source), TestBundle: bytes.NewReader(bundle)}); err != nil {
		t.Fatal(err)
	}
	_, err := ReadRequest(&wire, acceptHeader, func(_ RequestHeader, _ PayloadKind, _ Artifact, content io.Reader) error {
		buffer := make([]byte, 1)
		_, readErr := content.Read(buffer)
		return readErr
	})
	if err == nil {
		t.Fatal("ReadRequest() error = nil")
	}
}

func TestRequestNeverWritesAnOverLimitPayloadByte(t *testing.T) {
	content := []byte("exact")
	descriptor := descriptor(content)
	var destination bytes.Buffer
	err := copyExactAndHash(&destination, bytes.NewReader(append(bytes.Clone(content), 'x')), descriptor)
	if err == nil || !bytes.Equal(destination.Bytes(), content) {
		t.Fatalf("copyExactAndHash() error=%v destination=%q", err, destination.Bytes())
	}
}

func TestProtocolRejectsNonCanonicalOrUnknownHeader(t *testing.T) {
	raw := []byte(`{"schema":"ascendany.judge-control.request.v1", "unknown":true}`)
	var wire bytes.Buffer
	wire.WriteString(requestMagic)
	if err := binary.Write(&wire, binary.BigEndian, uint32(len(raw))); err != nil {
		t.Fatal(err)
	}
	wire.Write(raw)
	if _, err := ReadRequest(&wire, acceptHeader, func(RequestHeader, PayloadKind, Artifact, io.Reader) error { return nil }); err == nil {
		t.Fatal("ReadRequest() error = nil")
	}
}

func TestResponseRoundTripBindsOutput(t *testing.T) {
	output := []byte("compile diagnostics")
	header := ResponseHeader{
		Schema: ResponseSchemaV1, JobID: "11111111-1111-4111-8111-111111111111",
		Result: &Result{
			Schema: ResultSchemaV1, Verdict: judgecontract.VerdictCompileError,
			ResultManifest: json.RawMessage(`{"schema":"ascendany.oj.execution-manifest.v1"}`),
		},
	}
	var wire bytes.Buffer
	if err := WriteResponse(&wire, header, output); err != nil {
		t.Fatal(err)
	}
	decoded, received, err := ReadResponse(&wire, int64(len(output)))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Result == nil || decoded.Result.Verdict != judgecontract.VerdictCompileError || !bytes.Equal(received, output) {
		t.Fatalf("decoded=%#v output=%q", decoded, received)
	}
}

func requestHeader(source, stdin, bundle []byte) RequestHeader {
	header := RequestHeader{
		Schema:         RequestSchemaV1,
		JudgeJobID:     "11111111-1111-4111-8111-111111111111",
		SubmissionID:   "22222222-2222-4222-8222-222222222222",
		ProblemID:      "33333333-3333-4333-8333-333333333333",
		ProblemVersion: 1, Mode: judgecontract.SubmissionSubmit, LanguageID: judgecontract.LanguageCPP20,
		Source: descriptor(source), TestBundle: descriptor(bundle),
		ProblemSchema: judgecontract.ProblemSchemaV1,
		ProblemSpec:   json.RawMessage(`{"checker":"exact","schema":"ascendany.oj.problem-spec.v1"}`),
		TimeLimitMS:   1000, MemoryLimitBytes: 64 << 20, OutputLimitBytes: 1 << 20,
	}
	if stdin != nil {
		value := descriptor(stdin)
		header.Stdin = &value
	}
	return header
}

func descriptor(value []byte) Artifact {
	return Artifact{SHA256: digest(value), SizeBytes: int64(len(value))}
}

func digest(value []byte) string {
	valueDigest := sha256.Sum256(value)
	return hex.EncodeToString(valueDigest[:])
}

func acceptHeader(RequestHeader) error { return nil }
