package feedback

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"net/textproto"
	"strings"
	"testing"
	"time"
)

type smtpCredentialResolverFunc func(context.Context, string, string) (string, error)

func (resolver smtpCredentialResolverFunc) Resolve(ctx context.Context, reference, authority string) (string, error) {
	return resolver(ctx, reference, authority)
}

type fakeSMTPResult struct {
	payload    []byte
	auth       []byte
	serverName string
	err        error
}

func TestSMTPDeliveryProviderDeliversThroughVerifiedImplicitTLSAndSTARTTLS(t *testing.T) {
	t.Parallel()
	certificate, roots := smtpTestCertificate(t)
	fixedTime := time.Date(2026, 7, 18, 3, 4, 5, 0, time.UTC)
	for _, security := range []string{smtpSecurityImplicitTLS, smtpSecuritySTARTTLS} {
		security := security
		t.Run(security, func(t *testing.T) {
			t.Parallel()
			results := make(chan fakeSMTPResult, 1)
			dial := func(ctx context.Context, network, address string) (net.Conn, error) {
				if network != "tcp" || address != "smtp.example.test:2465" {
					t.Fatalf("network=%q address=%q", network, address)
				}
				clientConnection, serverConnection := net.Pipe()
				go func() {
					results <- runFakeSMTPServer(serverConnection, security, certificate)
				}()
				return clientConnection, nil
			}
			provider, err := newSMTPDeliveryProvider(
				smtpCredentialResolverFunc(func(_ context.Context, reference, authority string) (string, error) {
					if reference != "feedback.delivery.smtp" || authority != "smtp.example.test:2465" {
						t.Fatalf("reference=%q authority=%q", reference, authority)
					}
					return `{"username":"smtp-user","password":"s3cret value"}`, nil
				}),
				dial,
				newNetSMTPClient,
				roots,
				func() time.Time { return fixedTime },
			)
			if err != nil {
				t.Fatal(err)
			}

			delivery := validSMTPDelivery(security)
			receiptBytes, err := provider.Deliver(context.Background(), delivery)
			if err != nil {
				t.Fatal(err)
			}
			var result fakeSMTPResult
			select {
			case result = <-results:
			case <-time.After(2 * time.Second):
				t.Fatal("fake SMTP server did not finish")
			}
			if result.err != nil {
				t.Fatal(result.err)
			}
			if !bytes.Equal(result.auth, []byte("\x00smtp-user\x00s3cret value")) || result.serverName != "smtp.example.test" {
				t.Fatalf("auth=%q serverName=%q", result.auth, result.serverName)
			}
			assertSMTPMessage(t, result.payload, delivery, fixedTime)

			var receipt smtpReceipt
			if err := json.Unmarshal(receiptBytes, &receipt); err != nil {
				t.Fatal(err)
			}
			configuration, failure := parseSMTPConfiguration(delivery)
			if failure != nil {
				t.Fatal(failure)
			}
			expectedMessageID := smtpMessageID(testFeedbackID, "smtp.example.test")
			expectedPayload, failure := encodeSMTPMessage(delivery, configuration, expectedMessageID, fixedTime)
			if failure != nil {
				t.Fatal(failure)
			}
			digest := sha256.Sum256(expectedPayload)
			if receipt.MessageID != expectedMessageID || receipt.PayloadSHA256 != hex.EncodeToString(digest[:]) {
				t.Fatalf("receipt=%#v", receipt)
			}
		})
	}
}

func TestSMTPConfigurationIsExactCanonicalAndAuthorityBound(t *testing.T) {
	t.Parallel()
	valid := smtpConfigurationJSON(smtpSecurityImplicitTLS)
	tests := map[string]string{
		"wrong schema":    valid,
		"array":           `[]`,
		"duplicate":       `{"host":"smtp.example.test","host":"smtp2.example.test","port":465,"security":"implicit_tls","fromEmail":"sender@example.test","fromName":"AscendAny","toEmail":"receiver@example.test","timeoutMilliseconds":1000}`,
		"unknown":         `{"host":"smtp.example.test","port":465,"security":"implicit_tls","fromEmail":"sender@example.test","fromName":"AscendAny","toEmail":"receiver@example.test","timeoutMilliseconds":1000,"mode":"mail"}`,
		"missing":         `{"host":"smtp.example.test","port":465,"security":"implicit_tls","fromEmail":"sender@example.test","fromName":"AscendAny","timeoutMilliseconds":1000}`,
		"uppercase host":  `{"host":"SMTP.example.test","port":465,"security":"implicit_tls","fromEmail":"sender@example.test","fromName":"AscendAny","toEmail":"receiver@example.test","timeoutMilliseconds":1000}`,
		"zero port":       `{"host":"smtp.example.test","port":0,"security":"implicit_tls","fromEmail":"sender@example.test","fromName":"AscendAny","toEmail":"receiver@example.test","timeoutMilliseconds":1000}`,
		"fractional port": `{"host":"smtp.example.test","port":465.0,"security":"implicit_tls","fromEmail":"sender@example.test","fromName":"AscendAny","toEmail":"receiver@example.test","timeoutMilliseconds":1000}`,
		"security":        `{"host":"smtp.example.test","port":465,"security":"tls","fromEmail":"sender@example.test","fromName":"AscendAny","toEmail":"receiver@example.test","timeoutMilliseconds":1000}`,
		"from injection":  `{"host":"smtp.example.test","port":465,"security":"implicit_tls","fromEmail":"sender@example.test\r\nBcc:x@example.test","fromName":"AscendAny","toEmail":"receiver@example.test","timeoutMilliseconds":1000}`,
		"name injection":  `{"host":"smtp.example.test","port":465,"security":"implicit_tls","fromEmail":"sender@example.test","fromName":"AscendAny\r\nBcc: x@example.test","toEmail":"receiver@example.test","timeoutMilliseconds":1000}`,
		"recipient name":  `{"host":"smtp.example.test","port":465,"security":"implicit_tls","fromEmail":"sender@example.test","fromName":"AscendAny","toEmail":"Receiver <receiver@example.test>","timeoutMilliseconds":1000}`,
		"short timeout":   `{"host":"smtp.example.test","port":465,"security":"implicit_tls","fromEmail":"sender@example.test","fromName":"AscendAny","toEmail":"receiver@example.test","timeoutMilliseconds":99}`,
		"trailing":        valid + `{}`,
	}
	for name, document := range tests {
		name, document := name, document
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			delivery := DeliveryRequest{ConfigurationSchema: SMTPConfigurationSchema, Configuration: json.RawMessage(document)}
			if name == "wrong schema" {
				delivery.ConfigurationSchema = "ascendany.feedback_delivery.smtp.v2"
			}
			_, failure := parseSMTPConfiguration(delivery)
			if failure == nil || failure.Code != "smtp_configuration_invalid" || !failure.Permanent {
				t.Fatalf("failure=%#v", failure)
			}
		})
	}

	delivery := DeliveryRequest{ConfigurationSchema: SMTPConfigurationSchema, Configuration: json.RawMessage(valid)}
	configuration, failure := parseSMTPConfiguration(delivery)
	if failure != nil || configuration.Authority != "smtp.example.test:2465" {
		t.Fatalf("configuration=%#v failure=%v", configuration, failure)
	}
	authority, err := CanonicalSMTPAuthority("smtp.example.test", 2465)
	if err != nil || authority != configuration.Authority {
		t.Fatalf("authority=%q error=%v", authority, err)
	}
	if _, err := CanonicalSMTPAuthority("SMTP.example.test", 2465); err == nil {
		t.Fatal("uppercase authority accepted")
	}
}

func TestSMTPCredentialIsStrictJSONWithProviderOwnedGrammar(t *testing.T) {
	t.Parallel()
	valid, failure := parseSMTPCredential(`{"username":"smtp-user","password":" secret value "}`)
	if failure != nil || valid.Username != "smtp-user" || valid.Password != " secret value " {
		t.Fatalf("credential=%#v failure=%v", valid, failure)
	}
	for name, document := range map[string]string{
		"empty":               "",
		"array":               `[]`,
		"duplicate":           `{"username":"one","username":"two","password":"secret"}`,
		"unknown":             `{"username":"one","password":"secret","token":"x"}`,
		"missing":             `{"username":"one"}`,
		"username whitespace": `{"username":" one","password":"secret"}`,
		"empty password":      `{"username":"one","password":""}`,
		"nul password":        `{"username":"one","password":"bad\u0000secret"}`,
		"trailing":            `{"username":"one","password":"secret"}[]`,
	} {
		name, document := name, document
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, failure := parseSMTPCredential(document)
			if failure == nil || failure.Code != "smtp_credential_invalid" || !failure.Permanent {
				t.Fatalf("failure=%#v", failure)
			}
		})
	}
}

func TestSMTPDeliveryRejectsHeaderInjectionAndUnhydratedAttachments(t *testing.T) {
	t.Parallel()
	delivery := validSMTPDelivery(smtpSecurityImplicitTLS)
	delivery.Title = "valid\r\nBcc: attacker@example.test"
	if failure := validateSMTPDelivery(delivery); failure == nil || failure.Code != "smtp_request_invalid" {
		t.Fatalf("failure=%#v", failure)
	}
	delivery = validSMTPDelivery(smtpSecurityImplicitTLS)
	delivery.Attachments[0].Content = nil
	if failure := validateSMTPDelivery(delivery); failure == nil || failure.Code != "smtp_request_invalid" {
		t.Fatalf("failure=%#v", failure)
	}
}

func TestSMTPMessageEncodingIsDeterministicAndBoundarySafe(t *testing.T) {
	t.Parallel()
	delivery := validSMTPDelivery(smtpSecurityImplicitTLS)
	configuration, failure := parseSMTPConfiguration(delivery)
	if failure != nil {
		t.Fatal(failure)
	}
	date := time.Date(2026, 7, 18, 3, 4, 5, 0, time.UTC)
	messageID := smtpMessageID(delivery.FeedbackID, configuration.Host)
	first, failure := encodeSMTPMessage(delivery, configuration, messageID, date)
	if failure != nil {
		t.Fatal(failure)
	}
	second, failure := encodeSMTPMessage(delivery, configuration, messageID, date)
	if failure != nil || !bytes.Equal(first, second) {
		t.Fatalf("deterministic=%t failure=%v", bytes.Equal(first, second), failure)
	}
	assertSMTPMessage(t, first, delivery, date)
}

func TestSMTPDeliveryProviderRequiresDependencies(t *testing.T) {
	t.Parallel()
	resolver := smtpCredentialResolverFunc(func(context.Context, string, string) (string, error) { return "", nil })
	dial := smtpDialContext(func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("unused") })
	clientFactory := smtpClientFactory(func(net.Conn, string) (smtpClient, error) { return nil, errors.New("unused") })
	clock := func() time.Time { return time.Now() }
	for _, construct := range []func() (*SMTPDeliveryProvider, error){
		func() (*SMTPDeliveryProvider, error) {
			return newSMTPDeliveryProvider(nil, dial, clientFactory, nil, clock)
		},
		func() (*SMTPDeliveryProvider, error) {
			return newSMTPDeliveryProvider(resolver, nil, clientFactory, nil, clock)
		},
		func() (*SMTPDeliveryProvider, error) { return newSMTPDeliveryProvider(resolver, dial, nil, nil, clock) },
		func() (*SMTPDeliveryProvider, error) {
			return newSMTPDeliveryProvider(resolver, dial, clientFactory, nil, nil)
		},
	} {
		if _, err := construct(); CodeOf(err) != ErrorInvalidConfiguration {
			t.Fatalf("error=%v code=%q", err, CodeOf(err))
		}
	}
}

func validSMTPDelivery(security string) DeliveryRequest {
	studentNumber := "20260001"
	ptaNickname := "pta-user"
	platform := "linux"
	appVersion := "2.0.0"
	userAgent := "AscendAny Desktop/2"
	firstContent := []byte("first-image-content")
	secondContent := []byte("second-image-content")
	return DeliveryRequest{
		FeedbackID:          testFeedbackID,
		Title:               "导入页面建议",
		Content:             "请优化 <导入> 页面。\n第二行。",
		Platform:            &platform,
		AppVersion:          &appVersion,
		UserAgent:           &userAgent,
		ConfigurationID:     42,
		ConfigurationSchema: SMTPConfigurationSchema,
		Configuration:       json.RawMessage(smtpConfigurationJSON(security)),
		CredentialRef:       stringPointer("feedback.delivery.smtp"),
		Sender: DeliverySender{
			AccountID:     testAccountID,
			Username:      "student_01",
			DisplayName:   "张三",
			StudentNumber: &studentNumber,
			PTANickname:   &ptaNickname,
			Role:          "student",
		},
		Attachments: []DeliveryAttachment{
			smtpTestAttachment(1, "first.png", "image/png", firstContent),
			smtpTestAttachment(2, "second.jpeg", "image/jpeg", secondContent),
		},
	}
}

func smtpConfigurationJSON(security string) string {
	return fmt.Sprintf(`{"host":"smtp.example.test","port":2465,"security":%q,"fromEmail":"sender@example.test","fromName":"AscendAny Feedback","toEmail":"receiver@example.test","timeoutMilliseconds":5000}`, security)
}

func smtpTestAttachment(sequence int16, filename, mediaType string, content []byte) DeliveryAttachment {
	digest := sha256.Sum256(content)
	hash := hex.EncodeToString(digest[:])
	return DeliveryAttachment{
		Sequence: sequence, Filename: filename, SHA256: hash, SizeBytes: int64(len(content)),
		MediaType: mediaType, StorageKey: "sha256/" + hash[:2] + "/" + hash, Content: content,
	}
}

func stringPointer(value string) *string { return &value }

func smtpTestCertificate(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "smtp.example.test"},
		DNSNames:     []string{"smtp.example.test"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(parsed)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: privateKey, Leaf: parsed}, roots
}

func runFakeSMTPServer(connection net.Conn, security string, certificate tls.Certificate) fakeSMTPResult {
	defer connection.Close()
	serverTLS := &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
	if security == smtpSecurityImplicitTLS {
		tlsConnection := tls.Server(connection, serverTLS)
		if err := tlsConnection.Handshake(); err != nil {
			return fakeSMTPResult{err: err}
		}
		connection = tlsConnection
	}
	reader := bufio.NewReader(connection)
	writer := bufio.NewWriter(connection)
	if err := smtpServerWrite(writer, "220 smtp.example.test ESMTP ready\r\n"); err != nil {
		return fakeSMTPResult{err: err}
	}
	command, err := smtpServerRead(reader)
	if err != nil || !strings.HasPrefix(command, "EHLO ") {
		return fakeSMTPResult{err: fmt.Errorf("initial EHLO=%q error=%w", command, err)}
	}
	if security == smtpSecuritySTARTTLS {
		if err := smtpServerWrite(writer, "250-smtp.example.test\r\n250 STARTTLS\r\n"); err != nil {
			return fakeSMTPResult{err: err}
		}
		command, err = smtpServerRead(reader)
		if err != nil || command != "STARTTLS" {
			return fakeSMTPResult{err: fmt.Errorf("STARTTLS=%q error=%w", command, err)}
		}
		if err := smtpServerWrite(writer, "220 begin TLS\r\n"); err != nil {
			return fakeSMTPResult{err: err}
		}
		tlsConnection := tls.Server(connection, serverTLS)
		if err := tlsConnection.Handshake(); err != nil {
			return fakeSMTPResult{err: err}
		}
		connection = tlsConnection
		reader = bufio.NewReader(connection)
		writer = bufio.NewWriter(connection)
		command, err = smtpServerRead(reader)
		if err != nil || !strings.HasPrefix(command, "EHLO ") {
			return fakeSMTPResult{err: fmt.Errorf("post-TLS EHLO=%q error=%w", command, err)}
		}
	}
	if err := smtpServerWrite(writer, "250-smtp.example.test\r\n250 AUTH PLAIN\r\n"); err != nil {
		return fakeSMTPResult{err: err}
	}
	command, err = smtpServerRead(reader)
	fields := strings.Fields(command)
	if err != nil || len(fields) != 3 || fields[0] != "AUTH" || fields[1] != "PLAIN" {
		return fakeSMTPResult{err: fmt.Errorf("AUTH=%q error=%w", command, err)}
	}
	auth, err := base64.StdEncoding.DecodeString(fields[2])
	if err != nil {
		return fakeSMTPResult{err: err}
	}
	if err := smtpServerWrite(writer, "235 2.7.0 authenticated\r\n"); err != nil {
		return fakeSMTPResult{err: err}
	}
	command, err = smtpServerRead(reader)
	if err != nil || command != "MAIL FROM:<sender@example.test>" {
		return fakeSMTPResult{err: fmt.Errorf("MAIL=%q error=%w", command, err)}
	}
	if err := smtpServerWrite(writer, "250 sender accepted\r\n"); err != nil {
		return fakeSMTPResult{err: err}
	}
	command, err = smtpServerRead(reader)
	if err != nil || command != "RCPT TO:<receiver@example.test>" {
		return fakeSMTPResult{err: fmt.Errorf("RCPT=%q error=%w", command, err)}
	}
	if err := smtpServerWrite(writer, "250 recipient accepted\r\n"); err != nil {
		return fakeSMTPResult{err: err}
	}
	command, err = smtpServerRead(reader)
	if err != nil || command != "DATA" {
		return fakeSMTPResult{err: fmt.Errorf("DATA=%q error=%w", command, err)}
	}
	if err := smtpServerWrite(writer, "354 send message\r\n"); err != nil {
		return fakeSMTPResult{err: err}
	}
	payload, err := textproto.NewReader(reader).ReadDotBytes()
	if err != nil {
		return fakeSMTPResult{err: err}
	}
	if err := smtpServerWrite(writer, "250 queued\r\n"); err != nil {
		return fakeSMTPResult{err: err}
	}
	command, err = smtpServerRead(reader)
	if err != nil || command != "QUIT" {
		return fakeSMTPResult{err: fmt.Errorf("QUIT=%q error=%w", command, err)}
	}
	if err := smtpServerWrite(writer, "221 bye\r\n"); err != nil {
		return fakeSMTPResult{err: err}
	}
	state := connection.(*tls.Conn).ConnectionState()
	return fakeSMTPResult{payload: payload, auth: auth, serverName: state.ServerName}
}

func smtpServerRead(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	return strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"), err
}

func smtpServerWrite(writer *bufio.Writer, response string) error {
	if _, err := writer.WriteString(response); err != nil {
		return err
	}
	return writer.Flush()
}

func assertSMTPMessage(t *testing.T, payload []byte, delivery DeliveryRequest, date time.Time) {
	t.Helper()
	message, err := mail.ReadMessage(bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	decodedSubject, err := new(mime.WordDecoder).DecodeHeader(message.Header.Get("Subject"))
	if err != nil || decodedSubject != "[AscendAny 反馈] "+delivery.Title {
		t.Fatalf("subject=%q error=%v", decodedSubject, err)
	}
	from, err := mail.ParseAddress(message.Header.Get("From"))
	if err != nil || from.Name != "AscendAny Feedback" || from.Address != "sender@example.test" {
		t.Fatalf("from=%#v error=%v", from, err)
	}
	if message.Header.Get("Message-ID") != smtpMessageID(testFeedbackID, "smtp.example.test") ||
		message.Header.Get("X-AscendAny-Feedback-ID") != testFeedbackID ||
		message.Header.Get("X-AscendAny-Idempotency-Key") != testFeedbackID ||
		message.Header.Get("Date") != date.Format(time.RFC1123Z) {
		t.Fatalf("headers=%v", message.Header)
	}
	mediaType, parameters, err := mime.ParseMediaType(message.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/mixed" {
		t.Fatalf("content type=%q parameters=%v error=%v", mediaType, parameters, err)
	}
	mixed := multipart.NewReader(message.Body, parameters["boundary"])
	alternativePart, err := mixed.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	alternativeType, alternativeParameters, err := mime.ParseMediaType(alternativePart.Header.Get("Content-Type"))
	if err != nil || alternativeType != "multipart/alternative" {
		t.Fatalf("alternative=%q parameters=%v error=%v", alternativeType, alternativeParameters, err)
	}
	alternative := multipart.NewReader(alternativePart, alternativeParameters["boundary"])
	plainPart, err := alternative.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	plainEncoded, err := io.ReadAll(plainPart)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(plainEncoded, []byte("-")) {
		t.Fatalf("base64 plain part contains boundary alphabet sentinel: %q", plainEncoded)
	}
	plain := decodeSMTPBase64(t, plainEncoded)
	for _, expected := range []string{
		"AscendAny 用户反馈", "反馈 ID: " + testFeedbackID, "账号 ID: " + testAccountID,
		"用户名: student_01", "显示名称: 张三", "角色: student", "学号: 20260001", "PTA 昵称: pta-user",
		"平台: linux", "应用版本: 2.0.0", "User Agent: AscendAny Desktop/2", "附件数: 2",
		"发送时间: " + date.Format(time.RFC3339), "反馈内容：", delivery.Content,
	} {
		if !strings.Contains(string(plain), expected) {
			t.Fatalf("plain body missing %q:\n%s", expected, plain)
		}
	}
	htmlPart, err := alternative.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	htmlEncoded, err := io.ReadAll(htmlPart)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(htmlEncoded, []byte("-")) {
		t.Fatalf("base64 HTML part contains boundary alphabet sentinel: %q", htmlEncoded)
	}
	markup := decodeSMTPBase64(t, htmlEncoded)
	if !bytes.Contains(markup, []byte("AscendAny 用户反馈")) || !bytes.Contains(markup, []byte("请优化 &lt;导入&gt; 页面")) {
		t.Fatalf("HTML body=%s", markup)
	}
	if _, err := alternative.NextPart(); !errors.Is(err, io.EOF) {
		t.Fatalf("unexpected alternative part error=%v", err)
	}

	for index, expected := range delivery.Attachments {
		part, err := mixed.NextPart()
		if err != nil {
			t.Fatalf("attachment %d: %v", index+1, err)
		}
		_, dispositionParameters, err := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
		if err != nil || dispositionParameters["filename"] != expected.Filename {
			t.Fatalf("attachment %d headers=%v error=%v", index+1, part.Header, err)
		}
		if content := decodeSMTPBase64Reader(t, part); !bytes.Equal(content, expected.Content) {
			t.Fatalf("attachment %d content=%q", index+1, content)
		}
	}
	if _, err := mixed.NextPart(); !errors.Is(err, io.EOF) {
		t.Fatalf("unexpected mixed part error=%v", err)
	}
}

func decodeSMTPBase64Reader(t *testing.T, reader io.Reader) []byte {
	t.Helper()
	encoded, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	return decodeSMTPBase64(t, encoded)
}

func decodeSMTPBase64(t *testing.T, encoded []byte) []byte {
	t.Helper()
	compact := strings.NewReplacer("\r", "", "\n", "", " ", "", "\t", "").Replace(string(encoded))
	decoded, err := base64.StdEncoding.DecodeString(compact)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}
