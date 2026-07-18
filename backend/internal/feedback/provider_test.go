package feedback

import (
	"context"
	"errors"
	"testing"
)

type schemaProviderFunc func(context.Context, DeliveryRequest) ([]byte, error)

func (provider schemaProviderFunc) Deliver(ctx context.Context, delivery DeliveryRequest) ([]byte, error) {
	return provider(ctx, delivery)
}

func TestSchemaDeliveryProviderDispatchesOnlyExactSchemas(t *testing.T) {
	t.Parallel()
	var webhookCalls int
	var smtpCalls int
	provider, err := newSchemaDeliveryProvider(
		schemaProviderFunc(func(_ context.Context, delivery DeliveryRequest) ([]byte, error) {
			webhookCalls++
			if delivery.ConfigurationSchema != WebhookConfigurationSchema {
				t.Fatalf("webhook schema=%q", delivery.ConfigurationSchema)
			}
			return []byte("webhook"), nil
		}),
		schemaProviderFunc(func(_ context.Context, delivery DeliveryRequest) ([]byte, error) {
			smtpCalls++
			if delivery.ConfigurationSchema != SMTPConfigurationSchema {
				t.Fatalf("SMTP schema=%q", delivery.ConfigurationSchema)
			}
			return []byte("smtp"), nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	for schema, expected := range map[string]string{
		WebhookConfigurationSchema: "webhook",
		SMTPConfigurationSchema:    "smtp",
	} {
		receipt, err := provider.Deliver(context.Background(), DeliveryRequest{ConfigurationSchema: schema})
		if err != nil || string(receipt) != expected {
			t.Fatalf("schema=%q receipt=%q error=%v", schema, receipt, err)
		}
	}
	_, err = provider.Deliver(context.Background(), DeliveryRequest{ConfigurationSchema: "ascendany.feedback_delivery.smtp.v2"})
	var failure *ProviderFailure
	if !errors.As(err, &failure) || failure.Code != "delivery_schema_unsupported" || !failure.Permanent {
		t.Fatalf("failure=%#v error=%v", failure, err)
	}
	if webhookCalls != 1 || smtpCalls != 1 {
		t.Fatalf("webhook calls=%d SMTP calls=%d", webhookCalls, smtpCalls)
	}
}

func TestSchemaDeliveryProviderRequiresBothProviders(t *testing.T) {
	t.Parallel()
	valid := schemaProviderFunc(func(context.Context, DeliveryRequest) ([]byte, error) { return []byte("ok"), nil })
	if _, err := newSchemaDeliveryProvider(nil, valid); CodeOf(err) != ErrorInvalidConfiguration {
		t.Fatalf("nil webhook error=%v", err)
	}
	if _, err := newSchemaDeliveryProvider(valid, nil); CodeOf(err) != ErrorInvalidConfiguration {
		t.Fatalf("nil SMTP error=%v", err)
	}
}
