package chatagent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
)

func TestApplyNotesPatchOwnsExactUnifiedDiffCoordinates(t *testing.T) {
	t.Parallel()
	fixtures := []struct {
		name    string
		content string
		patch   string
		want    string
	}{
		{
			name: "replace line", content: "A\nB\nC",
			patch: "--- notes.md\n+++ notes.md\n@@ -1,3 +1,3 @@\n A\n-B\n+B2\n C", want: "A\nB2\nC",
		},
		{
			name: "multiple hunks", content: "A\nB\nC\nD\nF",
			patch: "--- notes.md\n+++ notes.md\n@@ -1,2 +1,2 @@\n A\n-B\n+B2\n@@ -4,2 +4,3 @@\n D\n+E\n F", want: "A\nB2\nC\nD\nE\nF",
		},
		{
			name: "insert into empty", content: "",
			patch: "--- notes.md\n+++ notes.md\n@@ -0,0 +1 @@\n+first", want: "first",
		},
		{
			name: "content resembles a file header", content: "-- notes.md\nA",
			patch: "--- notes.md\n+++ notes.md\n@@ -1,2 +1 @@\n--- notes.md\n A", want: "A",
		},
		{
			name: "convert missing newline to newline", content: "A",
			patch: "--- notes.md\n+++ notes.md\n@@ -1 +1 @@\n-A\n\\ No newline at end of file\n+A", want: "A\n",
		},
		{
			name: "remove newline", content: "A\n",
			patch: "--- notes.md\n+++ notes.md\n@@ -1 +1 @@\n-A\n+A\n\\ No newline at end of file", want: "A",
		},
	}
	for _, fixture := range fixtures {
		fixture := fixture
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			got, err := applyNotesPatch(fixture.content, fixture.patch)
			if err != nil || got != fixture.want {
				t.Fatalf("got=%q want=%q error=%v", got, fixture.want, err)
			}
		})
	}
}

func TestPersistedMessageContentUsesTypedFrontendContextBound(t *testing.T) {
	t.Parallel()
	var document agentFrontendV1RunContextDocument
	document.Schema = AgentFrontendV1ContextSchema
	document.CurrentUser.Content = "summarize"
	document.Messages = []agentFrontendV1ContextMessage{{Content: "summarize", Role: "user"}}
	document.Notes.Content = strings.Repeat("😀", MaxFrontendNotesCharacters)
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	canonical, _, err := canonicaljson.Object(raw, MaxFrontendContextDocumentBytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(canonical) <= MaxMessageBytes || !validPersistedMessageContent(MessageUser, string(canonical)) {
		t.Fatalf("typed context bytes=%d", len(canonical))
	}
	document.Notes.Content += "x"
	raw, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	canonical, _, err = canonicaljson.Object(raw, MaxFrontendContextDocumentBytes)
	if err != nil {
		t.Fatal(err)
	}
	if validPersistedMessageContent(MessageUser, string(canonical)) {
		t.Fatal("accepted frontend notes above the Unicode character limit")
	}
}

func TestApplyNotesPatchRejectsMalformedUnifiedDiff(t *testing.T) {
	t.Parallel()
	fixtures := map[string]string{
		"old count":        "--- notes.md\n+++ notes.md\n@@ -1,2 +1,1 @@\n A",
		"new count":        "--- notes.md\n+++ notes.md\n@@ -1,1 +1,2 @@\n A",
		"new start":        "--- notes.md\n+++ notes.md\n@@ -1,1 +2,1 @@\n A",
		"old zero start":   "--- notes.md\n+++ notes.md\n@@ -0,1 +1,1 @@\n-A\n+B",
		"hunk overlap":     "--- notes.md\n+++ notes.md\n@@ -2 +2 @@\n B\n@@ -1 +1 @@\n A",
		"duplicate header": "--- notes.md\n+++ notes.md\n@@ -1 +1 @@\n A\n--- notes.md\n+++ notes.md",
		"preamble":         "diff --git a/notes.md b/notes.md\n--- notes.md\n+++ notes.md\n@@ -1 +1 @@\n A",
		"stray marker":     "--- notes.md\n+++ notes.md\n@@ -1 +1 @@\n\\ No newline at end of file\n A",
		"wrong old marker": "--- notes.md\n+++ notes.md\n@@ -1 +1 @@\n A\n\\ No newline at end of file",
		"early new marker": "--- notes.md\n+++ notes.md\n@@ -1,1 +1,2 @@\n+A\n\\ No newline at end of file\n B",
		"empty hunk":       "--- notes.md\n+++ notes.md\n@@ -1,0 +1,0 @@",
		"extra operation":  "--- notes.md\n+++ notes.md\n@@ -1 +1 @@\n-A\n+B\n+C",
		"semantic no-op":   "--- notes.md\n+++ notes.md\n@@ -1 +1 @@\n-A\n+A",
	}
	for name, patch := range fixtures {
		name, patch := name, patch
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got, err := applyNotesPatch("A\nB\n", patch); err == nil {
				t.Fatalf("accepted malformed patch as %q", got)
			}
		})
	}
}

func TestFrontendNotesReplayRejectsChangedPreviousStateDigest(t *testing.T) {
	t.Parallel()
	state := FrontendNotesState{Content: "seed"}
	arguments := json.RawMessage(`{"content":"next","mode":"replace"}`)
	decoded, err := decodeUpdateNotesArguments(arguments)
	if err != nil {
		t.Fatal(err)
	}
	update, err := deriveNotesUpdate(state, decoded)
	if err != nil {
		t.Fatal(err)
	}
	result, err := encodeUpdateNotesResult(update)
	if err != nil {
		t.Fatal(err)
	}
	record := ToolCallRecord{
		Name: ToolUpdateNotes, ArgumentsSchema: UpdateNotesArgumentsSchema, Arguments: arguments,
		Outcome: ToolSucceeded, ResultSchema: runtimeStringPointer(UpdateNotesResultSchema), Result: result,
	}
	if replayed, err := replayFrontendNotes(&state, []ToolCallRecord{record}); err != nil || replayed == nil || replayed.Content != "next" {
		t.Fatalf("replayed=%#v error=%v", replayed, err)
	}
	changed := state
	changed.Content = "changed seed"
	if _, err := replayFrontendNotes(&changed, []ToolCallRecord{record}); err == nil || !strings.Contains(err.Error(), "deterministic replay") {
		t.Fatalf("error=%v", err)
	}
}
