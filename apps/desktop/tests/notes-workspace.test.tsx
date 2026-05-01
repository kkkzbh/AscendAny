import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { NotesWorkspace } from "@/components/notes/NotesWorkspace";
import { useNotesStore } from "@/stores/notesStore";

const upsertNote = vi.fn(async (payload: {
  id: string;
  title: string;
  content: string;
  titleIsAuto: boolean;
}) => ({
  id: payload.id,
  title: payload.title,
  content: payload.content,
  titleIsAuto: payload.titleIsAuto,
  createdAt: 1_700_000_000,
  updatedAt: 1_700_000_001,
}));

const setActiveNote = vi.fn(async () => true);
const clearNoteContent = vi.fn(async (id: string) => ({
  id,
  title: "原标题",
  content: "",
  titleIsAuto: false,
  createdAt: 1_700_000_000,
  updatedAt: 1_700_000_002,
}));

beforeEach(() => {
  upsertNote.mockClear();
  setActiveNote.mockClear();
  clearNoteContent.mockClear();
  window.electronAPI = {
    minimize: vi.fn(),
    maximize: vi.fn(),
    close: vi.fn(),
    platform: "linux",
    localStateUpsertNote: upsertNote,
    localStateSetActiveNote: setActiveNote,
    localStateClearNoteContent: clearNoteContent,
  } as unknown as ElectronAPI;
  Object.assign(navigator, {
    clipboard: { writeText: vi.fn().mockResolvedValue(undefined) },
  });
  useNotesStore.getState().resetForAccount();
  useNotesStore.getState().hydrateFromLocalState({
    items: [
      {
        id: "note_a",
        title: "原标题",
        content: "# 笔记内容\n要点 1\n要点 2",
        titleIsAuto: false,
        createdAt: 1_700_000_000,
        updatedAt: 1_700_000_000,
      },
      {
        id: "note_b",
        title: "另一份",
        content: "另外一份的摘要",
        titleIsAuto: false,
        createdAt: 1_700_000_000,
        updatedAt: 1_700_000_005,
      },
    ],
    activeNoteId: "note_a",
  });
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("NotesWorkspace", () => {
  it("renders markdown preview and the action toolbar by default", () => {
    render(<NotesWorkspace />);
    expect(screen.getByDisplayValue("原标题")).toBeTruthy();
    expect(screen.getByText("笔记内容")).toBeTruthy();
    expect(screen.getByRole("button", { name: "复制 Markdown" })).toBeTruthy();
  });

  it("switches to list view when 加载笔记 is clicked", () => {
    render(<NotesWorkspace />);
    fireEvent.click(screen.getByRole("button", { name: "加载笔记" }));
    expect(useNotesStore.getState().view).toBe("list");
    expect(screen.getByText("所有笔记")).toBeTruthy();
    expect(screen.getByText("另一份")).toBeTruthy();
  });

  it("clicking another note in list view switches active and returns to detail", () => {
    useNotesStore.getState().setView("list");
    render(<NotesWorkspace />);
    fireEvent.click(screen.getByText("另一份"));
    expect(setActiveNote).toHaveBeenCalledWith("note_b");
  });

  it("expands the actions menu and confirms the clear flow", async () => {
    render(<NotesWorkspace />);
    fireEvent.click(screen.getByRole("button", { name: "更多操作" }));
    expect(screen.getByRole("menuitem", { name: "导出为 md" })).toBeTruthy();
    fireEvent.click(screen.getByRole("menuitem", { name: "清空笔记" }));
    fireEvent.click(screen.getByRole("button", { name: "清空" }));
    await Promise.resolve();
    expect(clearNoteContent).toHaveBeenCalledWith("note_a");
  });
});
