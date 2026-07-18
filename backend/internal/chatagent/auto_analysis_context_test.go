package chatagent

import (
	"strings"
	"testing"
)

func TestAutoAnalysisInputContentRoundTripsEveryFrozenFrontendField(t *testing.T) {
	t.Parallel()
	frontendContext := testAutoAnalysisFrontendContext()
	content := mustAutoAnalysisInputContent(t, frontendContext)
	decoded, err := decodeAutoAnalysisInputContent(content)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != frontendContext {
		t.Fatalf("decoded=%#v want=%#v", decoded, frontendContext)
	}
	for _, value := range []string{
		frontendContext.StudentID,
		frontendContext.PTANickname,
		frontendContext.RoleID,
		frontendContext.RoleName,
		frontendContext.RoleSystemPrompt,
		frontendContext.LatestExamID,
		frontendContext.Notes,
		frontendContext.NotesTitle,
		AutoAnalysisFrontendContextSchema,
		AutoAnalysisInputContent,
	} {
		if !strings.Contains(content, value) {
			t.Fatalf("content omits %q: %s", value, content)
		}
	}
}

func TestAutoAnalysisInputContentRejectsNonCanonicalOrIncompleteDocuments(t *testing.T) {
	t.Parallel()
	for name, content := range map[string]string{
		"plain instruction": AutoAnalysisInputContent,
		"unknown field":     `{"context":{"latestExamId":"","notes":"","notesLocked":false,"notesTitle":"","ptaNickname":"","roleId":"","roleName":"","roleSystemPrompt":"","studentId":""},"instruction":"` + AutoAnalysisInputContent + `","schema":"` + AutoAnalysisFrontendContextSchema + `","unknown":true}`,
		"missing field":     `{"context":{"latestExamId":"","notes":"","notesTitle":"","ptaNickname":"","roleId":"","roleName":"","roleSystemPrompt":"","studentId":""},"instruction":"` + AutoAnalysisInputContent + `","schema":"` + AutoAnalysisFrontendContextSchema + `"}`,
	} {
		name, content := name, content
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := decodeAutoAnalysisInputContent(content); err == nil {
				t.Fatal("expected typed canonical document rejection")
			}
		})
	}
}

func testAutoAnalysisFrontendContext() AutoAnalysisFrontendContext {
	return AutoAnalysisFrontendContext{
		StudentID:        "20260001",
		PTANickname:      "pta-user",
		RoleID:           "role-7",
		RoleName:         "Coach",
		RoleSystemPrompt: "Focus on weak topics.",
		LatestExamID:     "99999999-9999-4999-8999-999999999999",
		Notes:            "Review graphs.",
		NotesTitle:       "Next steps",
		NotesLocked:      true,
	}
}

func mustAutoAnalysisInputContent(t testing.TB, frontendContext AutoAnalysisFrontendContext) string {
	t.Helper()
	content, err := canonicalAutoAnalysisInputContent(frontendContext)
	if err != nil {
		t.Fatal(err)
	}
	return content
}
