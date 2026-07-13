package httpapi

import (
	"net/http"

	"github.com/kkkzbh/AscendAny/backend/internal/configuration"
)

func (handler *Handler) authorizeKnowledgeCatalogPublication(writer http.ResponseWriter, request *http.Request) {
	if !handler.requireNoQuery(writer, request) {
		return
	}
	access, ok := bearerToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	}
	var payload configuration.CatalogPublicationAuthorizationInput
	if err := decodeStrictJSONWithLimit(
		writer,
		request,
		&payload,
		maxConfigurationJSONBytes,
		"Catalog publication authorization payload exceeds 327680 bytes.",
		"Catalog publication authorization request exceeded its duration limit.",
	); err != nil {
		handler.handleRequestContractError(writer, request, err)
		return
	}
	result, err := handler.configuration.AuthorizeKnowledgeCatalogPublication(request.Context(), access, payload)
	if err != nil {
		handler.handleConfigurationError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusCreated, result)
}
