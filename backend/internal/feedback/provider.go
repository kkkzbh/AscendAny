package feedback

import (
	"context"
	"errors"

	credentialdomain "github.com/kkkzbh/AscendAny/backend/internal/credential"
)

// SchemaDeliveryProvider owns the exact provider selected by the immutable
// feedback-delivery configuration schema.
type SchemaDeliveryProvider struct {
	webhook DeliveryProvider
	smtp    DeliveryProvider
}

func NewDeliveryProvider(credentials credentialdomain.Resolver) (*SchemaDeliveryProvider, error) {
	webhook, err := NewWebhookDeliveryProvider(credentials)
	if err != nil {
		return nil, err
	}
	smtpProvider, err := NewSMTPDeliveryProvider(credentials)
	if err != nil {
		return nil, err
	}
	return newSchemaDeliveryProvider(webhook, smtpProvider)
}

func newSchemaDeliveryProvider(webhook DeliveryProvider, smtpProvider DeliveryProvider) (*SchemaDeliveryProvider, error) {
	if webhook == nil || smtpProvider == nil {
		return nil, feedbackError(
			ErrorInvalidConfiguration,
			true,
			"construct schema feedback delivery provider",
			errors.New("webhook and SMTP delivery providers are required"),
		)
	}
	return &SchemaDeliveryProvider{webhook: webhook, smtp: smtpProvider}, nil
}

func (provider *SchemaDeliveryProvider) Deliver(ctx context.Context, delivery DeliveryRequest) ([]byte, error) {
	switch delivery.ConfigurationSchema {
	case WebhookConfigurationSchema:
		return provider.webhook.Deliver(ctx, delivery)
	case SMTPConfigurationSchema:
		return provider.smtp.Deliver(ctx, delivery)
	default:
		return nil, providerFailure("delivery_schema_unsupported", true, errors.New("feedback delivery schema is unsupported"))
	}
}
