package chatagent

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
)

const (
	AgentFrontendV1ContextSchema    = "ascendany.agent.frontend-context.v1"
	MaxFrontendNotesCharacters      = 32_768
	MaxFrontendNotesBytes           = MaxFrontendNotesCharacters * utf8.UTFMax
	MaxFrontendContextDocumentBytes = 512 << 10
	MaxRunEventDocumentBytes        = 1 << 20
)

const (
	ToolUpdateNotes                 = "update_notes"
	UpdateNotesArgumentsSchema      = "ascendany.agent_tool.update_notes_arguments.v1"
	UpdateNotesResultSchema         = "ascendany.agent_tool.update_notes_result.v1"
	updateNotesLockedErrorCode      = "notes_locked"
	updateNotesUnavailableErrorCode = "notes_context_unavailable"
	updateNotesArgumentsErrorCode   = "tool_arguments_invalid"
)

var notesHunkHeaderPattern = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@(?: .*)?$`)

type FrontendNotesState struct {
	Content string
	Title   string
	Locked  bool
}

type NotesUpdate struct {
	Mode     string  `json:"mode"`
	Previous string  `json:"previous"`
	Next     string  `json:"next"`
	Patch    *string `json:"patch"`
}

type agentFrontendV1ContextMessage struct {
	Content          string  `json:"content"`
	ReasoningContent *string `json:"reasoningContent"`
	Role             string  `json:"role"`
}

type agentFrontendV1RunContextDocument struct {
	CurrentUser struct {
		Content      string `json:"content"`
		MessageIndex int    `json:"messageIndex"`
		PTANickname  string `json:"ptaNickname"`
		StudentID    string `json:"studentId"`
	} `json:"currentUser"`
	Messages []agentFrontendV1ContextMessage `json:"messages"`
	Notes    struct {
		Content string `json:"content"`
		Locked  bool   `json:"locked"`
		Title   string `json:"title"`
	} `json:"notes"`
	Role struct {
		ID           string `json:"id"`
		Name         string `json:"name"`
		SystemPrompt string `json:"systemPrompt"`
	} `json:"role"`
	Schema  string `json:"schema"`
	Summary string `json:"summary"`
}

type updateNotesArguments struct {
	Content *string `json:"content"`
	Mode    *string `json:"mode"`
	Patch   *string `json:"patch"`
}

type updateNotesResult struct {
	Length         int    `json:"length"`
	Mode           string `json:"mode"`
	NextSHA256     string `json:"nextSha256"`
	OK             bool   `json:"ok"`
	PreviousSHA256 string `json:"previousSha256"`
	UpdatedNotes   string `json:"updatedNotes"`
}

func validFrontendNotesContent(content string) bool {
	return len(content) <= MaxFrontendNotesBytes && utf8.ValidString(content) &&
		utf8.RuneCountInString(content) <= MaxFrontendNotesCharacters && !strings.ContainsRune(content, '\x00')
}

func cloneFrontendNotesState(state *FrontendNotesState) *FrontendNotesState {
	if state == nil {
		return nil
	}
	owned := *state
	return &owned
}

func decodeReplyFrontendNotes(content string) (*FrontendNotesState, bool, error) {
	canonical, _, err := canonicaljson.Object(json.RawMessage(content), MaxFrontendContextDocumentBytes)
	if err != nil {
		return nil, false, nil
	}
	var identity struct {
		Schema string `json:"schema"`
	}
	if err := json.Unmarshal(canonical, &identity); err != nil || identity.Schema != AgentFrontendV1ContextSchema {
		return nil, false, nil
	}
	if !bytes.Equal(canonical, []byte(content)) {
		return nil, true, errors.New("Agent frontend input is not canonical JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	var document agentFrontendV1RunContextDocument
	if err := decoder.Decode(&document); err != nil || requireDecoderEOF(decoder) != nil || document.Schema != AgentFrontendV1ContextSchema {
		return nil, true, errors.New("Agent frontend input violates its typed document contract")
	}
	if !validFrontendNotesContent(document.Notes.Content) || len(document.Notes.Title) > 4096 ||
		!utf8.ValidString(document.Notes.Title) || strings.ContainsRune(document.Notes.Title, '\x00') {
		return nil, true, errors.New("Agent frontend notes violate their bounds")
	}
	return &FrontendNotesState{Content: document.Notes.Content, Title: document.Notes.Title, Locked: document.Notes.Locked}, true, nil
}

func requireDecoderEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("JSON document contains trailing data")
		}
		return err
	}
	return nil
}

func decodeUpdateNotesArguments(raw json.RawMessage) (updateNotesArguments, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var arguments updateNotesArguments
	if err := decoder.Decode(&arguments); err != nil || requireDecoderEOF(decoder) != nil || arguments.Mode == nil {
		return updateNotesArguments{}, errors.New("update_notes arguments violate their object contract")
	}
	switch *arguments.Mode {
	case "replace":
		if arguments.Content == nil || arguments.Patch != nil || !validFrontendNotesContent(*arguments.Content) {
			return updateNotesArguments{}, errors.New("replace requires one bounded content string")
		}
	case "patch":
		if arguments.Patch == nil || arguments.Content != nil || !validFrontendNotesContent(*arguments.Patch) {
			return updateNotesArguments{}, errors.New("patch requires one bounded unified diff string")
		}
	default:
		return updateNotesArguments{}, errors.New("mode must be patch or replace")
	}
	return arguments, nil
}

func deriveNotesUpdate(state FrontendNotesState, arguments updateNotesArguments) (NotesUpdate, error) {
	if state.Locked {
		return NotesUpdate{}, errors.New("notes are locked")
	}
	var update NotesUpdate
	update.Mode = *arguments.Mode
	update.Previous = state.Content
	if update.Mode == "replace" {
		update.Next = *arguments.Content
	} else {
		next, err := applyNotesPatch(state.Content, *arguments.Patch)
		if err != nil {
			return NotesUpdate{}, err
		}
		update.Next = next
		patch := *arguments.Patch
		update.Patch = &patch
	}
	if !validFrontendNotesContent(update.Next) {
		return NotesUpdate{}, errors.New("updated notes exceed the content limit")
	}
	return update, nil
}

func applyNotesPatch(content, patch string) (string, error) {
	patchLines := splitNotesLines(patch)
	if len(patchLines) < 3 {
		return "", errors.New("patch must contain file headers and at least one hunk")
	}
	if patchLines[0] != "--- notes.md" || patchLines[1] != "+++ notes.md" {
		return "", errors.New("patch must begin with the exact --- notes.md and +++ notes.md file headers")
	}
	if !strings.HasPrefix(patchLines[2], "@@") {
		return "", errors.New("patch must contain an @@ hunk")
	}

	baseLines := splitNotesLines(content)
	output := make([]string, 0, len(baseLines))
	sourceIndex := 0
	patchIndex := 2
	trailingNewline := strings.HasSuffix(content, "\n")
	changed := false
	for patchIndex < len(patchLines) {
		header := patchLines[patchIndex]
		match := notesHunkHeaderPattern.FindStringSubmatch(header)
		if match == nil {
			return "", errors.New("invalid hunk header")
		}
		oldStart, oldCount, newStart, newCount, err := parseNotesHunkCoordinates(match)
		if err != nil {
			return "", err
		}
		if oldCount == 0 && newCount == 0 {
			return "", errors.New("patch hunk is empty")
		}
		oldTarget, err := notesHunkTarget(oldStart, oldCount)
		if err != nil {
			return "", err
		}
		if oldTarget < sourceIndex {
			return "", errors.New("patch hunks are out of order")
		}
		if oldTarget > len(baseLines) || oldCount > len(baseLines)-oldTarget {
			return "", errors.New("patch hunk starts beyond the notes content")
		}
		output = append(output, baseLines[sourceIndex:oldTarget]...)
		sourceIndex = oldTarget
		newTarget, err := notesHunkTarget(newStart, newCount)
		if err != nil || newTarget != len(output) {
			return "", errors.New("patch hunk new-file position does not match prior output")
		}
		patchIndex++
		oldConsumed, newProduced := 0, 0
		oldNoNewline, newNoNewline := false, false
		oldMarkerPosition, newMarkerPosition := -1, -1
		var previousPrefix byte
		previousHadMarker := false
		for patchIndex < len(patchLines) && !strings.HasPrefix(patchLines[patchIndex], "@@") {
			line := patchLines[patchIndex]
			if oldConsumed == oldCount && newProduced == newCount && line != `\ No newline at end of file` {
				break
			}
			patchIndex++
			if line == `\ No newline at end of file` {
				if previousPrefix == 0 || previousHadMarker {
					return "", errors.New("newline marker lacks one preceding hunk line")
				}
				switch previousPrefix {
				case ' ':
					if oldNoNewline || newNoNewline {
						return "", errors.New("hunk repeats an end-of-file newline marker")
					}
					oldNoNewline, newNoNewline = true, true
					oldMarkerPosition, newMarkerPosition = oldConsumed, newProduced
				case '-':
					if oldNoNewline {
						return "", errors.New("hunk repeats an old-file newline marker")
					}
					oldNoNewline = true
					oldMarkerPosition = oldConsumed
				case '+':
					if newNoNewline {
						return "", errors.New("hunk repeats a new-file newline marker")
					}
					newNoNewline = true
					newMarkerPosition = newProduced
				}
				previousHadMarker = true
				continue
			}
			if line == "" {
				return "", errors.New("patch hunk line lacks an operation prefix")
			}
			previousPrefix = line[0]
			previousHadMarker = false
			value := line[1:]
			switch line[0] {
			case ' ':
				if oldConsumed == oldCount || newProduced == newCount {
					return "", errors.New("patch hunk contains more context than its header declares")
				}
				if sourceIndex >= len(baseLines) || baseLines[sourceIndex] != value {
					return "", errors.New("patch context does not match the current notes")
				}
				output = append(output, value)
				sourceIndex++
				oldConsumed++
				newProduced++
			case '-':
				if oldConsumed == oldCount {
					return "", errors.New("patch hunk contains more deletions than its header declares")
				}
				changed = true
				if sourceIndex >= len(baseLines) || baseLines[sourceIndex] != value {
					return "", errors.New("patch deletion does not match the current notes")
				}
				sourceIndex++
				oldConsumed++
			case '+':
				if newProduced == newCount {
					return "", errors.New("patch hunk contains more additions than its header declares")
				}
				changed = true
				output = append(output, value)
				newProduced++
			default:
				return "", errors.New("patch hunk contains an invalid operation prefix")
			}
		}
		if oldConsumed != oldCount || newProduced != newCount {
			return "", errors.New("patch hunk line counts differ from its header")
		}
		touchesOldEOF := sourceIndex == len(baseLines)
		touchesNewEOF := touchesOldEOF && patchIndex == len(patchLines)
		if (oldNoNewline && (oldMarkerPosition != oldCount || !touchesOldEOF || strings.HasSuffix(content, "\n"))) ||
			newNoNewline && (newMarkerPosition != newCount || !touchesNewEOF) {
			return "", errors.New("newline marker does not describe the corresponding end of file")
		}
		if touchesNewEOF {
			switch {
			case newNoNewline:
				trailingNewline = false
			case oldNoNewline:
				trailingNewline = len(output) > 0
			}
		}
	}
	output = append(output, baseLines[sourceIndex:]...)
	result := strings.Join(output, "\n")
	if trailingNewline && len(output) > 0 {
		result += "\n"
	}
	if !changed || result == content {
		return "", errors.New("patch contains no mutation")
	}
	return result, nil
}

func parseNotesHunkCoordinates(match []string) (int, int, int, int, error) {
	if len(match) != 5 {
		return 0, 0, 0, 0, errors.New("hunk header capture is invalid")
	}
	values := [4]int{}
	for index, raw := range match[1:] {
		if raw == "" {
			values[index] = 1
			continue
		}
		value, err := strconv.Atoi(raw)
		if err != nil {
			return 0, 0, 0, 0, errors.New("hunk coordinate exceeds the supported integer range")
		}
		values[index] = value
	}
	return values[0], values[1], values[2], values[3], nil
}

func notesHunkTarget(start, count int) (int, error) {
	if start < 0 || count < 0 || count > 0 && start == 0 {
		return 0, errors.New("hunk range is invalid")
	}
	if count == 0 {
		return start, nil
	}
	return start - 1, nil
}

func splitNotesLines(value string) []string {
	if value == "" {
		return []string{}
	}
	lines := strings.Split(value, "\n")
	if strings.HasSuffix(value, "\n") {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func notesContentSHA256(content string) string {
	digest := sha256.Sum256([]byte(content))
	return hex.EncodeToString(digest[:])
}

func encodeUpdateNotesResult(update NotesUpdate) (json.RawMessage, error) {
	result := updateNotesResult{
		Length: utf8.RuneCountInString(update.Next), Mode: update.Mode, OK: true,
		PreviousSHA256: notesContentSHA256(update.Previous), NextSHA256: notesContentSHA256(update.Next),
		UpdatedNotes: update.Next,
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	canonical, _, err := canonicaljson.Object(raw, MaxToolDocumentBytes)
	return canonical, err
}

func validateUpdateNotesResult(record ToolCallRecord, state FrontendNotesState) (NotesUpdate, error) {
	if record.Name != ToolUpdateNotes || record.ArgumentsSchema != UpdateNotesArgumentsSchema || record.Outcome != ToolSucceeded ||
		record.ResultSchema == nil || *record.ResultSchema != UpdateNotesResultSchema {
		return NotesUpdate{}, errors.New("stored update_notes metadata is invalid")
	}
	arguments, err := decodeUpdateNotesArguments(record.Arguments)
	if err != nil {
		return NotesUpdate{}, err
	}
	update, err := deriveNotesUpdate(state, arguments)
	if err != nil {
		return NotesUpdate{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(record.Result))
	decoder.DisallowUnknownFields()
	var result updateNotesResult
	if err := decoder.Decode(&result); err != nil || requireDecoderEOF(decoder) != nil || !result.OK || result.Mode != update.Mode ||
		result.Length != utf8.RuneCountInString(update.Next) || result.PreviousSHA256 != notesContentSHA256(update.Previous) ||
		result.NextSHA256 != notesContentSHA256(update.Next) || result.UpdatedNotes != update.Next {
		return NotesUpdate{}, errors.New("stored update_notes result differs from deterministic replay")
	}
	return update, nil
}

func replayFrontendNotes(initial *FrontendNotesState, calls []ToolCallRecord) (*FrontendNotesState, error) {
	state := cloneFrontendNotesState(initial)
	for _, record := range calls {
		if record.Name != ToolUpdateNotes || record.Outcome != ToolSucceeded {
			continue
		}
		if state == nil {
			return nil, errors.New("successful update_notes call has no bound frontend notes")
		}
		update, err := validateUpdateNotesResult(record, *state)
		if err != nil {
			return nil, err
		}
		state.Content = update.Next
	}
	return state, nil
}

func notesUpdateForNewRecord(state *FrontendNotesState, record ToolCallRecord) (*FrontendNotesState, *NotesUpdate, error) {
	next := cloneFrontendNotesState(state)
	if record.Name != ToolUpdateNotes || record.Outcome != ToolSucceeded {
		return next, nil, nil
	}
	if next == nil {
		return nil, nil, errors.New("successful update_notes call has no bound frontend notes")
	}
	update, err := validateUpdateNotesResult(record, *next)
	if err != nil {
		return nil, nil, err
	}
	next.Content = update.Next
	return next, &update, nil
}

func validateNotesUpdateEvent(record ToolCallRecord, update NotesUpdate) error {
	state := FrontendNotesState{Content: update.Previous}
	validated, err := validateUpdateNotesResult(record, state)
	if err != nil || validated.Mode != update.Mode || validated.Previous != update.Previous ||
		validated.Next != update.Next || !sameOptionalStringValue(validated.Patch, update.Patch) {
		return errors.New("notes update event differs from its immutable tool record")
	}
	return nil
}

func sameOptionalStringValue(left, right *string) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}
