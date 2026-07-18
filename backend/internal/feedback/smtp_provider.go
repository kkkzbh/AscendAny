package feedback

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	credentialdomain "github.com/kkkzbh/AscendAny/backend/internal/credential"
)

const (
	SMTPConfigurationSchema = "ascendany.feedback_delivery.smtp.v1"

	smtpSecurityImplicitTLS = "implicit_tls"
	smtpSecuritySTARTTLS    = "starttls"

	minSMTPTimeoutMilliseconds = int64(100)
	maxSMTPTimeoutMilliseconds = int64(120_000)
	maxSMTPConfigurationBytes  = 4096
	maxSMTPCredentialBytes     = 8192
	maxSMTPEmailBytes          = 320
	maxSMTPFromNameBytes       = 320
	maxSMTPUsernameBytes       = 320
	maxSMTPPasswordBytes       = 4096
	maxSMTPMessageBytes        = 96 << 20
	maxSMTPReceiptBytes        = 512
)

type smtpConfiguration struct {
	Host                string
	Port                uint16
	Security            string
	FromEmail           string
	FromName            string
	ToEmail             string
	TimeoutMilliseconds int64
	Authority           string
}

type smtpCredential struct {
	Username string
	Password string
}

type smtpReceipt struct {
	MessageID     string `json:"messageId"`
	PayloadSHA256 string `json:"payloadSha256"`
}

type smtpClient interface {
	Extension(string) (bool, string)
	StartTLS(*tls.Config) error
	TLSConnectionState() (tls.ConnectionState, bool)
	Auth(smtp.Auth) error
	Mail(string) error
	Rcpt(string) error
	Data() (io.WriteCloser, error)
	Quit() error
	Close() error
}

type smtpDialContext func(context.Context, string, string) (net.Conn, error)
type smtpClientFactory func(net.Conn, string) (smtpClient, error)

type SMTPDeliveryProvider struct {
	credentials credentialdomain.Resolver
	dial        smtpDialContext
	newClient   smtpClientFactory
	rootCAs     *x509.CertPool
	now         func() time.Time
}

func NewSMTPDeliveryProvider(credentials credentialdomain.Resolver) (*SMTPDeliveryProvider, error) {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	return newSMTPDeliveryProvider(credentials, dialer.DialContext, newNetSMTPClient, nil, time.Now)
}

func newSMTPDeliveryProvider(
	credentials credentialdomain.Resolver,
	dial smtpDialContext,
	newClient smtpClientFactory,
	rootCAs *x509.CertPool,
	now func() time.Time,
) (*SMTPDeliveryProvider, error) {
	if credentials == nil || dial == nil || newClient == nil || now == nil {
		return nil, feedbackError(
			ErrorInvalidConfiguration,
			true,
			"construct SMTP delivery provider",
			errors.New("credential resolver, dialer, SMTP client factory, and clock are required"),
		)
	}
	return &SMTPDeliveryProvider{
		credentials: credentials,
		dial:        dial,
		newClient:   newClient,
		rootCAs:     rootCAs,
		now:         now,
	}, nil
}

func newNetSMTPClient(connection net.Conn, host string) (smtpClient, error) {
	return smtp.NewClient(connection, host)
}

func (provider *SMTPDeliveryProvider) Deliver(ctx context.Context, delivery DeliveryRequest) ([]byte, error) {
	if ctx == nil {
		return nil, providerFailure("smtp_request_invalid", true, errors.New("delivery context is required"))
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	configuration, failure := parseSMTPConfiguration(delivery)
	if failure != nil {
		return nil, failure
	}
	if failure := validateSMTPDelivery(delivery); failure != nil {
		return nil, failure
	}

	credentialDocument, err := provider.credentials.Resolve(ctx, *delivery.CredentialRef, configuration.Authority)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return nil, contextErr
		}
		return nil, providerFailure("smtp_credential_unavailable", true, errors.New("SMTP credential resolution failed"))
	}
	credential, failure := parseSMTPCredential(credentialDocument)
	if failure != nil {
		return nil, failure
	}

	messageID := smtpMessageID(delivery.FeedbackID, configuration.Host)
	payload, failure := encodeSMTPMessage(delivery, configuration, messageID, provider.now().UTC().Truncate(time.Second))
	if failure != nil {
		return nil, failure
	}

	requestContext, cancel := context.WithTimeout(ctx, time.Duration(configuration.TimeoutMilliseconds)*time.Millisecond)
	defer cancel()
	if err := provider.send(requestContext, ctx, configuration, credential, payload); err != nil {
		return nil, err
	}

	digest := sha256.Sum256(payload)
	receipt, err := json.Marshal(smtpReceipt{
		MessageID:     messageID,
		PayloadSHA256: hex.EncodeToString(digest[:]),
	})
	if err != nil || len(receipt) == 0 || len(receipt) > maxSMTPReceiptBytes {
		return nil, providerFailure("smtp_receipt_invalid", true, errors.New("SMTP receipt exceeds its encoding contract"))
	}
	return receipt, nil
}

func (provider *SMTPDeliveryProvider) send(
	requestContext context.Context,
	parentContext context.Context,
	configuration smtpConfiguration,
	credential smtpCredential,
	payload []byte,
) error {
	connection, err := provider.dial(requestContext, "tcp", configuration.Authority)
	if err != nil {
		return classifySMTPFailure(requestContext, parentContext, "connect", err)
	}
	if connection == nil {
		return providerFailure("smtp_transport_failure", false, errors.New("SMTP dialer returned no connection"))
	}
	defer connection.Close()
	deadline, hasDeadline := requestContext.Deadline()
	if !hasDeadline || connection.SetDeadline(deadline) != nil {
		return providerFailure("smtp_transport_failure", false, errors.New("SMTP connection deadline could not be established"))
	}
	stopCancellationInterrupt := context.AfterFunc(requestContext, func() {
		_ = connection.SetDeadline(time.Now())
	})
	defer stopCancellationInterrupt()

	tlsConfiguration := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: configuration.Host,
		RootCAs:    provider.rootCAs,
	}
	client, err := provider.openClient(requestContext, parentContext, connection, configuration, tlsConfiguration)
	if err != nil {
		return err
	}
	defer client.Close()

	state, tlsEstablished := client.TLSConnectionState()
	if !tlsEstablished || !state.HandshakeComplete || state.Version < tls.VersionTLS12 {
		return providerFailure("smtp_tls_rejected", true, errors.New("SMTP TLS state violates the minimum protocol contract"))
	}
	authSupported, mechanisms := client.Extension("AUTH")
	if !authSupported || !smtpAuthMechanismAdvertised(mechanisms, "PLAIN") {
		return providerFailure("smtp_plain_auth_unavailable", true, errors.New("SMTP server does not advertise AUTH PLAIN after TLS"))
	}
	if err := client.Auth(smtp.PlainAuth("", credential.Username, credential.Password, configuration.Host)); err != nil {
		return classifySMTPFailure(requestContext, parentContext, "authenticate", err)
	}
	if err := client.Mail(configuration.FromEmail); err != nil {
		return classifySMTPFailure(requestContext, parentContext, "sender", err)
	}
	if err := client.Rcpt(configuration.ToEmail); err != nil {
		return classifySMTPFailure(requestContext, parentContext, "recipient", err)
	}
	dataWriter, err := client.Data()
	if err != nil {
		return classifySMTPFailure(requestContext, parentContext, "message", err)
	}
	if _, err := dataWriter.Write(payload); err != nil {
		_ = dataWriter.Close()
		return classifySMTPFailure(requestContext, parentContext, "message", err)
	}
	if err := dataWriter.Close(); err != nil {
		return classifySMTPFailure(requestContext, parentContext, "message", err)
	}
	// A successful DATA close is the SMTP acceptance boundary. QUIT is best-effort
	// because retrying an accepted message after a failed QUIT would duplicate it.
	_ = client.Quit()
	return nil
}

func (provider *SMTPDeliveryProvider) openClient(
	requestContext context.Context,
	parentContext context.Context,
	connection net.Conn,
	configuration smtpConfiguration,
	tlsConfiguration *tls.Config,
) (smtpClient, error) {
	if configuration.Security == smtpSecurityImplicitTLS {
		tlsConnection := tls.Client(connection, tlsConfiguration)
		if err := tlsConnection.HandshakeContext(requestContext); err != nil {
			return nil, classifySMTPFailure(requestContext, parentContext, "tls", err)
		}
		client, err := provider.newClient(tlsConnection, configuration.Host)
		if err != nil {
			return nil, classifySMTPFailure(requestContext, parentContext, "greeting", err)
		}
		return client, nil
	}

	client, err := provider.newClient(connection, configuration.Host)
	if err != nil {
		return nil, classifySMTPFailure(requestContext, parentContext, "greeting", err)
	}
	startTLSSupported, _ := client.Extension("STARTTLS")
	if !startTLSSupported {
		_ = client.Close()
		return nil, providerFailure("smtp_starttls_unavailable", true, errors.New("SMTP server does not advertise STARTTLS"))
	}
	if err := client.StartTLS(tlsConfiguration); err != nil {
		_ = client.Close()
		return nil, classifySMTPFailure(requestContext, parentContext, "tls", err)
	}
	return client, nil
}

func parseSMTPConfiguration(delivery DeliveryRequest) (smtpConfiguration, *ProviderFailure) {
	if delivery.ConfigurationSchema != SMTPConfigurationSchema {
		return smtpConfiguration{}, providerFailure("smtp_configuration_invalid", true, errors.New("SMTP configuration schema is unsupported"))
	}
	if len(delivery.Configuration) == 0 || len(delivery.Configuration) > maxSMTPConfigurationBytes {
		return smtpConfiguration{}, providerFailure("smtp_configuration_invalid", true, errors.New("SMTP configuration document is unbounded"))
	}

	decoder := json.NewDecoder(bytes.NewReader(delivery.Configuration))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return smtpConfiguration{}, providerFailure("smtp_configuration_invalid", true, errors.New("SMTP configuration must be an object"))
	}
	var configuration smtpConfiguration
	seen := make(map[string]struct{}, 7)
	for decoder.More() {
		keyToken, err := decoder.Token()
		key, keyOK := keyToken.(string)
		if err != nil || !keyOK {
			return smtpConfiguration{}, providerFailure("smtp_configuration_invalid", true, errors.New("SMTP configuration key is invalid"))
		}
		if _, exists := seen[key]; exists {
			return smtpConfiguration{}, providerFailure("smtp_configuration_invalid", true, errors.New("SMTP configuration contains a duplicate field"))
		}
		seen[key] = struct{}{}
		switch key {
		case "host":
			if err := decoder.Decode(&configuration.Host); err != nil {
				return smtpConfiguration{}, providerFailure("smtp_configuration_invalid", true, errors.New("SMTP host must be a string"))
			}
		case "port":
			if err := decoder.Decode(&configuration.Port); err != nil {
				return smtpConfiguration{}, providerFailure("smtp_configuration_invalid", true, errors.New("SMTP port must be an integer"))
			}
		case "security":
			if err := decoder.Decode(&configuration.Security); err != nil {
				return smtpConfiguration{}, providerFailure("smtp_configuration_invalid", true, errors.New("SMTP security must be a string"))
			}
		case "fromEmail":
			if err := decoder.Decode(&configuration.FromEmail); err != nil {
				return smtpConfiguration{}, providerFailure("smtp_configuration_invalid", true, errors.New("SMTP fromEmail must be a string"))
			}
		case "fromName":
			if err := decoder.Decode(&configuration.FromName); err != nil {
				return smtpConfiguration{}, providerFailure("smtp_configuration_invalid", true, errors.New("SMTP fromName must be a string"))
			}
		case "toEmail":
			if err := decoder.Decode(&configuration.ToEmail); err != nil {
				return smtpConfiguration{}, providerFailure("smtp_configuration_invalid", true, errors.New("SMTP toEmail must be a string"))
			}
		case "timeoutMilliseconds":
			if err := decoder.Decode(&configuration.TimeoutMilliseconds); err != nil {
				return smtpConfiguration{}, providerFailure("smtp_configuration_invalid", true, errors.New("SMTP timeout must be an integer"))
			}
		default:
			return smtpConfiguration{}, providerFailure("smtp_configuration_invalid", true, errors.New("SMTP configuration contains an unknown field"))
		}
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') {
		return smtpConfiguration{}, providerFailure("smtp_configuration_invalid", true, errors.New("SMTP configuration object is incomplete"))
	}
	if _, err := decoder.Token(); err != io.EOF {
		return smtpConfiguration{}, providerFailure("smtp_configuration_invalid", true, errors.New("SMTP configuration contains trailing data"))
	}
	for _, required := range []string{"host", "port", "security", "fromEmail", "fromName", "toEmail", "timeoutMilliseconds"} {
		if _, exists := seen[required]; !exists {
			return smtpConfiguration{}, providerFailure("smtp_configuration_invalid", true, fmt.Errorf("SMTP configuration field %s is required", required))
		}
	}
	if !validSMTPHost(configuration.Host) || configuration.Port == 0 ||
		(configuration.Security != smtpSecurityImplicitTLS && configuration.Security != smtpSecuritySTARTTLS) ||
		!validSMTPMailbox(configuration.FromEmail) || !validSMTPMailbox(configuration.ToEmail) ||
		!validSMTPDisplayName(configuration.FromName) ||
		configuration.TimeoutMilliseconds < minSMTPTimeoutMilliseconds ||
		configuration.TimeoutMilliseconds > maxSMTPTimeoutMilliseconds {
		return smtpConfiguration{}, providerFailure("smtp_configuration_invalid", true, errors.New("SMTP configuration violates its canonical bounds"))
	}
	configuration.Authority = net.JoinHostPort(configuration.Host, strconv.FormatUint(uint64(configuration.Port), 10))
	if !credentialdomain.ValidAuthority(configuration.Authority) {
		return smtpConfiguration{}, providerFailure("smtp_configuration_invalid", true, errors.New("SMTP authority is not canonical"))
	}
	return configuration, nil
}

func parseSMTPCredential(document string) (smtpCredential, *ProviderFailure) {
	if len(document) == 0 || len(document) > maxSMTPCredentialBytes {
		return smtpCredential{}, providerFailure("smtp_credential_invalid", true, errors.New("SMTP credential document is unbounded"))
	}
	decoder := json.NewDecoder(strings.NewReader(document))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return smtpCredential{}, providerFailure("smtp_credential_invalid", true, errors.New("SMTP credential must be an object"))
	}
	var credential smtpCredential
	seen := make(map[string]struct{}, 2)
	for decoder.More() {
		keyToken, err := decoder.Token()
		key, keyOK := keyToken.(string)
		if err != nil || !keyOK {
			return smtpCredential{}, providerFailure("smtp_credential_invalid", true, errors.New("SMTP credential key is invalid"))
		}
		if _, exists := seen[key]; exists {
			return smtpCredential{}, providerFailure("smtp_credential_invalid", true, errors.New("SMTP credential contains a duplicate field"))
		}
		seen[key] = struct{}{}
		switch key {
		case "username":
			if err := decoder.Decode(&credential.Username); err != nil {
				return smtpCredential{}, providerFailure("smtp_credential_invalid", true, errors.New("SMTP username must be a string"))
			}
		case "password":
			if err := decoder.Decode(&credential.Password); err != nil {
				return smtpCredential{}, providerFailure("smtp_credential_invalid", true, errors.New("SMTP password must be a string"))
			}
		default:
			return smtpCredential{}, providerFailure("smtp_credential_invalid", true, errors.New("SMTP credential contains an unknown field"))
		}
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') {
		return smtpCredential{}, providerFailure("smtp_credential_invalid", true, errors.New("SMTP credential object is incomplete"))
	}
	if _, err := decoder.Token(); err != io.EOF {
		return smtpCredential{}, providerFailure("smtp_credential_invalid", true, errors.New("SMTP credential contains trailing data"))
	}
	if _, exists := seen["username"]; !exists {
		return smtpCredential{}, providerFailure("smtp_credential_invalid", true, errors.New("SMTP username is required"))
	}
	if _, exists := seen["password"]; !exists {
		return smtpCredential{}, providerFailure("smtp_credential_invalid", true, errors.New("SMTP password is required"))
	}
	if credential.Username == "" || len(credential.Username) > maxSMTPUsernameBytes ||
		credential.Username != strings.TrimSpace(credential.Username) || !utf8.ValidString(credential.Username) ||
		strings.IndexByte(credential.Username, 0) >= 0 || credential.Password == "" ||
		len(credential.Password) > maxSMTPPasswordBytes || !utf8.ValidString(credential.Password) ||
		strings.IndexByte(credential.Password, 0) >= 0 {
		return smtpCredential{}, providerFailure("smtp_credential_invalid", true, errors.New("SMTP credential violates its field bounds"))
	}
	return credential, nil
}

func validateSMTPDelivery(delivery DeliveryRequest) *ProviderFailure {
	if !canonicalUUIDv4.MatchString(delivery.FeedbackID) || delivery.ConfigurationID <= 0 ||
		delivery.CredentialRef == nil || !configurationKey.MatchString(*delivery.CredentialRef) ||
		delivery.Title == "" || len(delivery.Title) > MaxTitleBytes || delivery.Title != strings.TrimSpace(delivery.Title) ||
		!validSMTPHeaderText(delivery.Title) || delivery.Content == "" || len(delivery.Content) > MaxContentBytes ||
		delivery.Content != strings.TrimSpace(delivery.Content) || !utf8.ValidString(delivery.Content) ||
		strings.IndexByte(delivery.Content, 0) >= 0 {
		return providerFailure("smtp_request_invalid", true, errors.New("stored feedback delivery violates its SMTP contract"))
	}
	for _, optional := range []struct {
		value *string
		limit int
	}{
		{delivery.Platform, MaxPlatformBytes},
		{delivery.AppVersion, MaxAppVersionBytes},
		{delivery.UserAgent, MaxUserAgentBytes},
	} {
		if optional.value != nil && (len(*optional.value) > optional.limit || *optional.value != strings.TrimSpace(*optional.value) ||
			!utf8.ValidString(*optional.value) || strings.IndexByte(*optional.value, 0) >= 0) {
			return providerFailure("smtp_request_invalid", true, errors.New("stored feedback metadata violates its SMTP contract"))
		}
	}
	if err := validateDeliverySender(delivery.Sender); err != nil {
		return providerFailure("smtp_request_invalid", true, errors.New("stored feedback sender violates its SMTP contract"))
	}
	if err := validateHydratedDeliveryAttachments(delivery.Attachments); err != nil {
		return providerFailure("smtp_request_invalid", true, errors.New("hydrated feedback attachments violate their SMTP contract"))
	}
	for _, attachment := range delivery.Attachments {
		if !validSMTPHeaderText(attachment.Filename) {
			return providerFailure("smtp_request_invalid", true, errors.New("attachment filename cannot form a MIME header"))
		}
	}
	return nil
}

func encodeSMTPMessage(
	delivery DeliveryRequest,
	configuration smtpConfiguration,
	messageID string,
	date time.Time,
) ([]byte, *ProviderFailure) {
	if date.IsZero() {
		return nil, providerFailure("smtp_request_invalid", true, errors.New("SMTP message date is required"))
	}
	mixedBoundary := "ascendany-mixed-" + strings.ReplaceAll(delivery.FeedbackID, "-", "")
	alternativeBoundary := "ascendany-alternative-" + strings.ReplaceAll(delivery.FeedbackID, "-", "")
	plainBody, htmlBody := smtpMessageBodies(delivery, date)

	var message bytes.Buffer
	mixedWriter := multipart.NewWriter(&message)
	if err := mixedWriter.SetBoundary(mixedBoundary); err != nil {
		return nil, providerFailure("smtp_request_invalid", true, errors.New("SMTP mixed boundary is invalid"))
	}
	fromHeader := encodeSMTPDisplayName(configuration.FromName) + " <" + configuration.FromEmail + ">"
	if !validSMTPHeaderText(configuration.ToEmail) || !validSMTPHeaderText(messageID) {
		return nil, providerFailure("smtp_request_invalid", true, errors.New("SMTP message header is invalid"))
	}
	writeSMTPHeader(&message, "Date", date.Format(time.RFC1123Z))
	writeSMTPHeader(&message, "Message-ID", messageID)
	writeSMTPHeader(&message, "X-AscendAny-Feedback-ID", delivery.FeedbackID)
	writeSMTPHeader(&message, "X-AscendAny-Idempotency-Key", delivery.FeedbackID)
	writeSMTPHeader(&message, "From", fromHeader)
	writeSMTPHeader(&message, "To", configuration.ToEmail)
	writeSMTPHeader(&message, "Subject", encodeSMTPHeaderWord("[AscendAny 反馈] "+delivery.Title))
	writeSMTPHeader(&message, "MIME-Version", "1.0")
	writeSMTPHeader(&message, "Content-Type", mime.FormatMediaType("multipart/mixed", map[string]string{"boundary": mixedBoundary}))
	message.WriteString("\r\n")

	alternativeHeader := make(textproto.MIMEHeader)
	alternativeHeader.Set("Content-Type", mime.FormatMediaType("multipart/alternative", map[string]string{"boundary": alternativeBoundary}))
	alternativePart, err := mixedWriter.CreatePart(alternativeHeader)
	if err != nil {
		return nil, providerFailure("smtp_request_invalid", true, errors.New("SMTP alternative part could not be created"))
	}
	alternativeWriter := multipart.NewWriter(alternativePart)
	if err := alternativeWriter.SetBoundary(alternativeBoundary); err != nil {
		return nil, providerFailure("smtp_request_invalid", true, errors.New("SMTP alternative boundary is invalid"))
	}
	if err := writeSMTPTextPart(alternativeWriter, "text/plain", []byte(plainBody)); err != nil {
		return nil, providerFailure("smtp_request_invalid", true, errors.New("SMTP plain text part could not be encoded"))
	}
	if err := writeSMTPTextPart(alternativeWriter, "text/html", []byte(htmlBody)); err != nil {
		return nil, providerFailure("smtp_request_invalid", true, errors.New("SMTP HTML part could not be encoded"))
	}
	if err := alternativeWriter.Close(); err != nil {
		return nil, providerFailure("smtp_request_invalid", true, errors.New("SMTP alternative part could not be closed"))
	}

	for _, attachment := range delivery.Attachments {
		header := make(textproto.MIMEHeader)
		header.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": attachment.Filename}))
		header.Set("Content-ID", fmt.Sprintf("<attachment-%d-%s@%s>", attachment.Sequence, delivery.FeedbackID, smtpMessageIDDomain(configuration.Host)))
		header.Set("Content-Transfer-Encoding", "base64")
		header.Set("Content-Type", mime.FormatMediaType(attachment.MediaType, map[string]string{"name": attachment.Filename}))
		part, err := mixedWriter.CreatePart(header)
		if err != nil || writeSMTPBase64(part, attachment.Content) != nil {
			return nil, providerFailure("smtp_request_invalid", true, errors.New("SMTP attachment could not be encoded"))
		}
	}
	if err := mixedWriter.Close(); err != nil {
		return nil, providerFailure("smtp_request_invalid", true, errors.New("SMTP mixed message could not be closed"))
	}
	if message.Len() == 0 || message.Len() > maxSMTPMessageBytes {
		return nil, providerFailure("smtp_request_invalid", true, errors.New("SMTP message exceeds its deterministic payload bound"))
	}
	return message.Bytes(), nil
}

func smtpMessageBodies(delivery DeliveryRequest, date time.Time) (string, string) {
	metadata := [][2]string{
		{"反馈 ID", delivery.FeedbackID},
		{"标题", delivery.Title},
		{"账号 ID", delivery.Sender.AccountID},
		{"用户名", delivery.Sender.Username},
		{"显示名称", delivery.Sender.DisplayName},
		{"角色", delivery.Sender.Role},
		{"学号", optionalSMTPValue(delivery.Sender.StudentNumber)},
		{"PTA 昵称", optionalSMTPValue(delivery.Sender.PTANickname)},
		{"平台", optionalSMTPValue(delivery.Platform)},
		{"应用版本", optionalSMTPValue(delivery.AppVersion)},
		{"User Agent", optionalSMTPValue(delivery.UserAgent)},
		{"附件数", strconv.Itoa(len(delivery.Attachments))},
		{"发送时间", date.Format(time.RFC3339)},
	}
	var plain strings.Builder
	plain.WriteString("AscendAny 用户反馈\r\n\r\n")
	for _, entry := range metadata {
		plain.WriteString(entry[0])
		plain.WriteString(": ")
		plain.WriteString(entry[1])
		plain.WriteString("\r\n")
	}
	plain.WriteString("\r\n反馈内容：\r\n")
	plain.WriteString(delivery.Content)
	plain.WriteString("\r\n")

	var markup strings.Builder
	markup.WriteString("<!doctype html><html><body><h1>AscendAny 用户反馈</h1><table>")
	for _, entry := range metadata {
		markup.WriteString("<tr><th align=\"left\">")
		markup.WriteString(html.EscapeString(entry[0]))
		markup.WriteString("</th><td>")
		markup.WriteString(html.EscapeString(entry[1]))
		markup.WriteString("</td></tr>")
	}
	markup.WriteString("</table><h2>反馈内容</h2><pre style=\"white-space:pre-wrap\">")
	markup.WriteString(html.EscapeString(delivery.Content))
	markup.WriteString("</pre></body></html>")
	return plain.String(), markup.String()
}

func writeSMTPTextPart(writer *multipart.Writer, mediaType string, content []byte) error {
	header := make(textproto.MIMEHeader)
	header.Set("Content-Transfer-Encoding", "base64")
	header.Set("Content-Type", mime.FormatMediaType(mediaType, map[string]string{"charset": "utf-8"}))
	part, err := writer.CreatePart(header)
	if err != nil {
		return err
	}
	return writeSMTPBase64(part, content)
}

func writeSMTPBase64(writer io.Writer, content []byte) error {
	const rawLineBytes = 57
	encoded := make([]byte, base64.StdEncoding.EncodedLen(rawLineBytes))
	for offset := 0; offset < len(content); offset += rawLineBytes {
		end := min(offset+rawLineBytes, len(content))
		line := content[offset:end]
		encodedLength := base64.StdEncoding.EncodedLen(len(line))
		base64.StdEncoding.Encode(encoded[:encodedLength], line)
		if offset > 0 {
			if _, err := io.WriteString(writer, "\r\n"); err != nil {
				return err
			}
		}
		if _, err := writer.Write(encoded[:encodedLength]); err != nil {
			return err
		}
	}
	return nil
}

func writeSMTPHeader(writer *bytes.Buffer, name, value string) {
	writer.WriteString(name)
	writer.WriteString(": ")
	writer.WriteString(value)
	writer.WriteString("\r\n")
}

func encodeSMTPDisplayName(value string) string {
	return encodeSMTPHeaderWord(value)
}

func encodeSMTPHeaderWord(value string) string {
	const maximumChunkBytes = 30
	var encoded strings.Builder
	remaining := value
	for len(remaining) > 0 {
		chunkLength := min(maximumChunkBytes, len(remaining))
		for chunkLength > 0 && !utf8.ValidString(remaining[:chunkLength]) {
			chunkLength--
		}
		if chunkLength == 0 {
			chunkLength = len(remaining)
		}
		if encoded.Len() > 0 {
			encoded.WriteString("\r\n\t")
		}
		encoded.WriteString("=?UTF-8?B?")
		encoded.WriteString(base64.StdEncoding.EncodeToString([]byte(remaining[:chunkLength])))
		encoded.WriteString("?=")
		remaining = remaining[chunkLength:]
	}
	return encoded.String()
}

func validSMTPHost(host string) bool {
	if host == "" || len(host) > 253 || host != strings.ToLower(host) || host != strings.TrimSpace(host) {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.String() == host
	}
	return credentialdomain.ValidAuthority(net.JoinHostPort(host, "1"))
}

func validSMTPMailbox(value string) bool {
	if value == "" || len(value) > maxSMTPEmailBytes || value != strings.TrimSpace(value) || !validSMTPHeaderText(value) {
		return false
	}
	for index := range len(value) {
		if value[index] >= utf8.RuneSelf {
			return false
		}
	}
	address, err := mail.ParseAddress(value)
	return err == nil && address.Name == "" && address.Address == value
}

func validSMTPDisplayName(value string) bool {
	return value != "" && len(value) <= maxSMTPFromNameBytes && value == strings.TrimSpace(value) && validSMTPHeaderText(value)
}

func validSMTPHeaderText(value string) bool {
	if !utf8.ValidString(value) || strings.ContainsAny(value, "\r\n\x00") {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func smtpAuthMechanismAdvertised(advertisement, mechanism string) bool {
	for _, candidate := range strings.Fields(advertisement) {
		if strings.EqualFold(candidate, mechanism) {
			return true
		}
	}
	return false
}

func smtpMessageID(feedbackID, host string) string {
	return "<feedback-" + feedbackID + "@" + smtpMessageIDDomain(host) + ">"
}

func smtpMessageIDDomain(host string) string {
	if net.ParseIP(host) != nil {
		return "[" + host + "]"
	}
	return host
}

func optionalSMTPValue(value *string) string {
	if value == nil {
		return "(none)"
	}
	return *value
}

func classifySMTPFailure(requestContext, parentContext context.Context, stage string, err error) error {
	if parentErr := parentContext.Err(); parentErr != nil {
		return parentErr
	}
	if requestContext.Err() != nil {
		return providerFailure("smtp_timeout", false, errors.New("SMTP request exceeded its configured timeout"))
	}
	var certificateInvalid x509.CertificateInvalidError
	var hostnameInvalid x509.HostnameError
	var unknownAuthority x509.UnknownAuthorityError
	if errors.As(err, &certificateInvalid) || errors.As(err, &hostnameInvalid) || errors.As(err, &unknownAuthority) {
		return providerFailure("smtp_tls_rejected", true, errors.New("SMTP TLS identity was rejected"))
	}
	var dnsError *net.DNSError
	if errors.As(err, &dnsError) && dnsError.IsNotFound {
		return providerFailure("smtp_dns_not_found", true, errors.New("SMTP host does not exist"))
	}
	var protocolError *textproto.Error
	if errors.As(err, &protocolError) {
		if protocolError.Code >= 400 && protocolError.Code < 500 {
			return providerFailure("smtp_temporarily_unavailable", false, errors.New("SMTP server returned a temporary failure"))
		}
		if protocolError.Code >= 500 && protocolError.Code < 600 {
			codes := map[string]string{
				"authenticate": "smtp_auth_rejected",
				"sender":       "smtp_sender_rejected",
				"recipient":    "smtp_recipient_rejected",
				"message":      "smtp_message_rejected",
			}
			code := codes[stage]
			if code == "" {
				code = "smtp_protocol_rejected"
			}
			return providerFailure(code, true, errors.New("SMTP server returned a permanent rejection"))
		}
		return providerFailure("smtp_protocol_rejected", true, errors.New("SMTP server returned an unsupported status"))
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return providerFailure("smtp_timeout", false, errors.New("SMTP transport timed out"))
	}
	return providerFailure("smtp_transport_failure", false, errors.New("SMTP transport failed"))
}

// CanonicalSMTPAuthority returns the exact destination identity used to bind
// one SMTP credential file.
func CanonicalSMTPAuthority(host string, port uint16) (string, error) {
	if !validSMTPHost(host) || port == 0 {
		return "", errors.New("SMTP host and port must be canonical")
	}
	authority := net.JoinHostPort(host, strconv.FormatUint(uint64(port), 10))
	if !credentialdomain.ValidAuthority(authority) {
		return "", errors.New("SMTP authority must be canonical")
	}
	return authority, nil
}
