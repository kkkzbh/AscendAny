package configuration

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
)

const MaximumCatalogPublicationRequestBytes = 4096

func ValidateCatalogPublicationIntent(intent CatalogPublicationIntent) error {
	if !validCatalogPublicationIntent(intent) {
		return errors.New("catalog publication intent violates the release contract")
	}
	return nil
}

func CanonicalCatalogPublicationRequest(request AuthorizedCatalogPublicationRequest) (json.RawMessage, error) {
	if !canonicalUUIDv4.MatchString(request.AuthorizationID) {
		return nil, errors.New("catalog publication authorization identity must be one canonical UUIDv4")
	}
	if err := ValidateCatalogPublicationIntent(request.CatalogPublicationIntent); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("encode catalog publication request: %w", err)
	}
	canonical, _, err := canonicaljson.Object(raw, MaximumCatalogPublicationRequestBytes)
	if err != nil {
		return nil, fmt.Errorf("canonicalize catalog publication request: %w", err)
	}
	return canonical, nil
}

func ParseCatalogPublicationRequest(raw json.RawMessage) (AuthorizedCatalogPublicationRequest, error) {
	canonical, _, err := canonicaljson.Object(raw, MaximumCatalogPublicationRequestBytes)
	if err != nil || !bytes.Equal(raw, canonical) {
		return AuthorizedCatalogPublicationRequest{}, errors.New("catalog publication request must be canonical JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var request AuthorizedCatalogPublicationRequest
	if err := decoder.Decode(&request); err != nil {
		return AuthorizedCatalogPublicationRequest{}, fmt.Errorf("decode catalog publication request: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return AuthorizedCatalogPublicationRequest{}, errors.New("catalog publication request contains a trailing value")
	}
	reencoded, err := CanonicalCatalogPublicationRequest(request)
	if err != nil || !bytes.Equal(reencoded, raw) {
		return AuthorizedCatalogPublicationRequest{}, errors.New("catalog publication request violates the release contract")
	}
	return request, nil
}
