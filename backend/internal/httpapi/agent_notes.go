package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/kkkzbh/AscendAny/backend/internal/agentnotes"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

const (
	maxAgentNoteDocumentJSONBytes int64 = 1 << 20
	maxAgentNoteStateJSONBytes    int64 = 1024
)

func (handler *Handler) listAgentNotes(writer http.ResponseWriter, request *http.Request) {
	cursor, limit, err := parseCursorPageQuery(
		request.URL.RawQuery,
		request.URL.ForceQuery,
		agentnotes.DefaultPageSize,
		agentnotes.MaxPageSize,
		agentnotes.ValidCursor,
	)
	if err != nil {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_agent_note_query", "Agent note query is invalid.")
		return
	}
	access, ok := bearerToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	}
	page, err := handler.agentNotes.List(request.Context(), access, cursor, limit)
	if err != nil {
		handler.handleAgentNotesError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, page)
}

func (handler *Handler) getAgentNote(writer http.ResponseWriter, request *http.Request) {
	if !handler.requireNoQuery(writer, request) {
		return
	}
	noteID := request.PathValue("noteId")
	if !agentnotes.ValidPublicID(noteID) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_agent_note_id", "Agent note ID is invalid.")
		return
	}
	access, ok := bearerToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	}
	note, found, err := handler.agentNotes.Get(request.Context(), access, noteID)
	if err != nil {
		handler.handleAgentNotesError(writer, request, err)
		return
	}
	if !found {
		handler.writeAPIError(writer, request, http.StatusNotFound, "agent_note_not_found", "Agent note does not exist.")
		return
	}
	writeJSON(writer, http.StatusOK, note)
}

func (handler *Handler) createAgentNote(writer http.ResponseWriter, request *http.Request) {
	if !handler.requireWritesEnabled(writer, request) || !handler.requireNoQuery(writer, request) {
		return
	}
	access, ok := bearerToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	}
	var input agentnotes.CreateInput
	if err := decodeStrictJSONWithLimit(
		writer,
		request,
		&input,
		maxAgentNoteDocumentJSONBytes,
		"Agent note payload exceeds 1048576 bytes.",
		"Agent note request body exceeded its duration limit.",
	); err != nil {
		handler.handleRequestContractError(writer, request, err)
		return
	}
	result, err := handler.agentNotes.Create(request.Context(), access, input)
	if err != nil {
		handler.handleAgentNotesError(writer, request, err)
		return
	}
	writer.Header().Set("Location", "/api/v2/students/me/notes/"+result.Note.ID)
	status := http.StatusCreated
	if result.Idempotent {
		status = http.StatusOK
	}
	writeJSON(writer, status, result)
}

func (handler *Handler) replaceAgentNote(writer http.ResponseWriter, request *http.Request) {
	if !handler.requireWritesEnabled(writer, request) || !handler.requireNoQuery(writer, request) {
		return
	}
	noteID := request.PathValue("noteId")
	if !agentnotes.ValidPublicID(noteID) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_agent_note_id", "Agent note ID is invalid.")
		return
	}
	access, ok := bearerToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	}
	var input agentnotes.ReplaceInput
	if err := decodeStrictJSONWithLimit(
		writer,
		request,
		&input,
		maxAgentNoteDocumentJSONBytes,
		"Agent note payload exceeds 1048576 bytes.",
		"Agent note request body exceeded its duration limit.",
	); err != nil {
		handler.handleRequestContractError(writer, request, err)
		return
	}
	result, err := handler.agentNotes.Replace(request.Context(), access, noteID, input)
	if err != nil {
		handler.handleAgentNotesError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func (handler *Handler) archiveAgentNote(writer http.ResponseWriter, request *http.Request) {
	handler.changeAgentNoteState(writer, request, true)
}

func (handler *Handler) restoreAgentNote(writer http.ResponseWriter, request *http.Request) {
	handler.changeAgentNoteState(writer, request, false)
}

func (handler *Handler) changeAgentNoteState(writer http.ResponseWriter, request *http.Request, archive bool) {
	if !handler.requireWritesEnabled(writer, request) || !handler.requireNoQuery(writer, request) {
		return
	}
	noteID := request.PathValue("noteId")
	if !agentnotes.ValidPublicID(noteID) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_agent_note_id", "Agent note ID is invalid.")
		return
	}
	access, ok := bearerToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	}
	var input agentnotes.StateInput
	if err := decodeStrictJSONWithLimit(
		writer,
		request,
		&input,
		maxAgentNoteStateJSONBytes,
		"Agent note state payload exceeds 1024 bytes.",
		"Agent note request body exceeded its duration limit.",
	); err != nil {
		handler.handleRequestContractError(writer, request, err)
		return
	}
	var result agentnotes.MutationResult
	var err error
	if archive {
		result, err = handler.agentNotes.Archive(request.Context(), access, noteID, input)
	} else {
		result, err = handler.agentNotes.Restore(request.Context(), access, noteID, input)
	}
	if err != nil {
		handler.handleAgentNotesError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func (handler *Handler) handleAgentNotesError(writer http.ResponseWriter, request *http.Request, err error) {
	if auth.ErrorCodeOf(err) != "" {
		handler.handleAuthError(writer, request, err)
		return
	}
	switch agentnotes.CodeOf(err) {
	case agentnotes.ErrorInvalidQuery:
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_agent_note_request", "Agent note request is invalid.")
		return
	case agentnotes.ErrorPrincipalRejected:
		handler.writeAPIError(writer, request, http.StatusForbidden, "auth_forbidden", "Authorization was rejected.")
		return
	case agentnotes.ErrorNotFound:
		handler.writeAPIError(writer, request, http.StatusNotFound, "agent_note_not_found", "Agent note does not exist.")
		return
	case agentnotes.ErrorCursorInvalid:
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_agent_note_cursor", "Agent note cursor is invalid.")
		return
	case agentnotes.ErrorHeadConflict:
		handler.writeAPIError(writer, request, http.StatusConflict, "agent_note_head_conflict", "Agent note head revision changed concurrently.")
		return
	case agentnotes.ErrorStateConflict:
		handler.writeAPIError(writer, request, http.StatusConflict, "agent_note_state_conflict", "Agent note state does not allow this mutation.")
		return
	case agentnotes.ErrorIdempotencyConflict:
		handler.writeAPIError(writer, request, http.StatusConflict, "agent_note_idempotency_conflict", "Agent note mutation identity conflicts with stored input.")
		return
	case agentnotes.ErrorCanceled:
		if errors.Is(context.Cause(request.Context()), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			handler.writeAPIError(writer, request, http.StatusRequestTimeout, "request_timeout", "Request exceeded its duration limit.")
			return
		}
		if errors.Is(err, context.Canceled) {
			handler.writeAPIError(writer, request, http.StatusBadRequest, "request_canceled", "Request was canceled.")
			return
		}
	}
	handler.logger.ErrorContext(request.Context(), "agent notes HTTP operation failed",
		"request_id", requestID(request.Context()),
		"code", agentnotes.CodeOf(err),
	)
	handler.writeAPIError(writer, request, http.StatusInternalServerError, "internal_error", "Request could not be completed.")
}
