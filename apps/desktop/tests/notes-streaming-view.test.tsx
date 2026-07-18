import { cleanup, render } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import { NotesStreamingView } from "@/components/notes/NotesStreamingView";
import type { NoteStreamState } from "@/stores/notesStore";

function makeStreamingState(
  overrides: Partial<NoteStreamState> = {},
): NoteStreamState {
  return {
    noteId: "note_a",
    mode: "patch",
    baseContent: "# 标题",
    targetContent: "# 标题\n- **新增重点**",
    segments: [
      { kind: "unchanged", lines: ["# 标题"] },
      { kind: "added", lines: ["- **新增重点**"] },
    ],
    totalAddedChars: "- **新增重点**".length,
    charsPerTick: 4,
    typedChars: "- **新增重点**".length,
    phase: "typing",
    startedAt: 1710000000000,
    ...overrides,
  };
}

describe("NotesStreamingView", () => {
  afterEach(() => {
    cleanup();
  });

  it("renders typing updates as live markdown preview", () => {
    const { container } = render(<NotesStreamingView streaming={makeStreamingState()} />);

    expect(container.querySelector(".notes-streaming-preview")).toBeTruthy();
    expect(container.querySelector(".chat-markdown-streaming")).toBeTruthy();
    expect(container.querySelector("h1")?.textContent).toBe("标题");
    expect(container.querySelector('li [data-streamdown="strong"]')?.textContent).toBe("新增重点");
  });

  it("renders fading updates as markdown without streaming affordances", () => {
    const { container } = render(
      <NotesStreamingView streaming={makeStreamingState({ phase: "fading" })} />,
    );

    expect(container.querySelector(".notes-streaming-preview")).toBeTruthy();
    expect(container.querySelector(".chat-markdown-streaming")).toBeNull();
    expect(container.querySelector('li [data-streamdown="strong"]')?.textContent).toBe("新增重点");
  });

  it("keeps deleting phase as a diff view", () => {
    const state = makeStreamingState({
      phase: "deleting",
      segments: [
        { kind: "unchanged", lines: ["保留"] },
        { kind: "removed", lines: ["删除"] },
      ],
      totalAddedChars: 0,
      typedChars: 0,
    });

    const { container } = render(<NotesStreamingView streaming={state} />);

    expect(container.querySelector(".notes-streaming-preview")).toBeNull();
    expect(container.querySelector(".notes-line--removed")?.textContent).toContain("删除");
    expect(container.textContent).toContain("笔记更新中…");
  });
});
