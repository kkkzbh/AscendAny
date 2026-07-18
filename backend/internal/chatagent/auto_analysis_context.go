package chatagent

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
)

const AutoAnalysisFrontendContextSchema = "ascendany.agent.auto-analysis.frontend-context.v1"

const (
	maximumAutoAnalysisStudentIDBytes   = 256
	maximumAutoAnalysisPTANicknameBytes = 256
	maximumAutoAnalysisRoleIDBytes      = 256
	maximumAutoAnalysisRoleNameBytes    = 4096
	maximumAutoAnalysisLatestExamBytes  = 4096
	maximumAutoAnalysisNotesTitleBytes  = 4096
)

type autoAnalysisInputDocument struct {
	Context     AutoAnalysisFrontendContext `json:"context"`
	Instruction string                      `json:"instruction"`
	Schema      string                      `json:"schema"`
}

func canonicalAutoAnalysisInputContent(context AutoAnalysisFrontendContext) (string, error) {
	if err := validateAutoAnalysisFrontendContext(context); err != nil {
		return "", err
	}
	raw, err := json.Marshal(autoAnalysisInputDocument{
		Context:     context,
		Instruction: AutoAnalysisInputContent,
		Schema:      AutoAnalysisFrontendContextSchema,
	})
	if err != nil {
		return "", err
	}
	canonical, _, err := canonicaljson.Object(raw, MaxFrontendContextDocumentBytes)
	if err != nil {
		return "", err
	}
	return string(canonical), nil
}

func decodeAutoAnalysisInputContent(content string) (AutoAnalysisFrontendContext, error) {
	canonical, _, err := canonicaljson.Object(json.RawMessage(content), MaxFrontendContextDocumentBytes)
	if err != nil || !bytes.Equal(canonical, []byte(content)) {
		return AutoAnalysisFrontendContext{}, errors.New("automatic analysis input is not canonical JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	var document autoAnalysisInputDocument
	if err := decoder.Decode(&document); err != nil || document.Schema != AutoAnalysisFrontendContextSchema ||
		document.Instruction != AutoAnalysisInputContent {
		return AutoAnalysisFrontendContext{}, errors.New("automatic analysis input violates its typed document contract")
	}
	expected, err := canonicalAutoAnalysisInputContent(document.Context)
	if err != nil || expected != content {
		return AutoAnalysisFrontendContext{}, errors.New("automatic analysis input violates its canonical field contract")
	}
	return document.Context, nil
}

func validateAutoAnalysisFrontendContext(context AutoAnalysisFrontendContext) error {
	if !validFrontendNotesContent(context.Notes) {
		return errors.New("automatic analysis frontend notes violate their bounds")
	}
	for _, field := range []struct {
		value   string
		maximum int
	}{
		{context.StudentID, maximumAutoAnalysisStudentIDBytes},
		{context.PTANickname, maximumAutoAnalysisPTANicknameBytes},
		{context.RoleID, maximumAutoAnalysisRoleIDBytes},
		{context.RoleName, maximumAutoAnalysisRoleNameBytes},
		{context.RoleSystemPrompt, MaxMessageBytes},
		{context.LatestExamID, maximumAutoAnalysisLatestExamBytes},
		{context.NotesTitle, maximumAutoAnalysisNotesTitleBytes},
	} {
		if len(field.value) > field.maximum || !utf8.ValidString(field.value) || strings.ContainsRune(field.value, '\x00') {
			return errors.New("automatic analysis frontend context contains an invalid field")
		}
	}
	return nil
}
