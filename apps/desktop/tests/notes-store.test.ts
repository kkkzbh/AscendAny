import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useNotesStore, deriveAutoNoteTitle, NOTES_LIMITS } from "@/stores/notesStore";

const upsertNote = vi.fn(async (payload: { id: string; title: string; content: string; titleIsAuto: boolean }) => ({
  id: payload.id,
  title: payload.title,
  content: payload.content,
  titleIsAuto: payload.titleIsAuto,
  createdAt: 1_700_000_000,
  updatedAt: Date.now(),
}));

const createNote = vi.fn(async () => ({
  id: "note_new",
  title: "",
  content: "",
  titleIsAuto: true,
  createdAt: Date.now(),
  updatedAt: Date.now(),
}));

const deleteNote = vi.fn(async (_id: string) => ({ activeNoteId: "note_a" }));

const setActiveNote = vi.fn(async () => true);

const clearNoteContent = vi.fn(async (id: string) => ({
  id,
  title: "保留标题",
  content: "",
  titleIsAuto: false,
  createdAt: 1_700_000_000,
  updatedAt: Date.now(),
}));

beforeEach(() => {
  vi.useFakeTimers({ now: 1_700_000_000_000 });
  upsertNote.mockClear();
  createNote.mockClear();
  deleteNote.mockClear();
  setActiveNote.mockClear();
  clearNoteContent.mockClear();
  window.electronAPI = {
    minimize: vi.fn(),
    maximize: vi.fn(),
    close: vi.fn(),
    platform: "linux",
    localStateUpsertNote: upsertNote,
    localStateCreateNote: createNote,
    localStateDeleteNote: deleteNote,
    localStateSetActiveNote: setActiveNote,
    localStateClearNoteContent: clearNoteContent,
  } as unknown as ElectronAPI;
  useNotesStore.getState().resetForAccount();
  useNotesStore.getState().hydrateFromLocalState({
    items: [
      {
        id: "note_a",
        title: "原标题",
        content: "原内容",
        titleIsAuto: true,
        createdAt: 1_700_000_000,
        updatedAt: 1_700_000_000,
      },
      {
        id: "note_b",
        title: "另一份",
        content: "其它",
        titleIsAuto: false,
        createdAt: 1_700_000_000,
        updatedAt: 1_700_000_001,
      },
    ],
    activeNoteId: "note_a",
  });
});

afterEach(() => {
  vi.useRealTimers();
});

describe("notesStore", () => {
  it("derives auto title from H1 then first non-empty line", () => {
    expect(deriveAutoNoteTitle("# 大标题\n正文")).toBe("大标题");
    expect(deriveAutoNoteTitle("正文第一行\n第二行")).toBe("正文第一行");
    expect(deriveAutoNoteTitle("")).toBe("");
  });

  it("hydrates with active note and ordering by updatedAt desc", () => {
    const state = useNotesStore.getState();
    expect(state.activeId).toBe("note_a");
    expect(state.order).toEqual(["note_b", "note_a"]);
    expect(state.items.note_b?.titleIsAuto).toBe(false);
  });

  it("debounces persistence on setContent and updates auto title", async () => {
    useNotesStore.getState().setContent("# 新标题\n第一段");
    expect(upsertNote).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(NOTES_LIMITS.PERSIST_DEBOUNCE_MS + 10);
    expect(upsertNote).toHaveBeenCalledTimes(1);
    const arg = upsertNote.mock.calls[0]?.[0];
    expect(arg?.title).toBe("新标题");
    expect(arg?.titleIsAuto).toBe(true);
  });

  it("locks title once user edits it manually", async () => {
    useNotesStore.getState().setTitle("自定义");
    await vi.advanceTimersByTimeAsync(NOTES_LIMITS.PERSIST_DEBOUNCE_MS + 10);
    expect(upsertNote).toHaveBeenLastCalledWith(
      expect.objectContaining({ title: "自定义", titleIsAuto: false }),
    );
    upsertNote.mockClear();
    useNotesStore.getState().setContent("# 不应改变标题\n正文");
    await vi.advanceTimersByTimeAsync(NOTES_LIMITS.PERSIST_DEBOUNCE_MS + 10);
    const last = upsertNote.mock.calls.at(-1)?.[0];
    expect(last?.title).toBe("自定义");
  });

  it("applyRemoteUpdate buffers when user is editing dirty content", async () => {
    useNotesStore.getState().setIsEditingContent(true);
    useNotesStore.getState().setContent("用户输入中…");
    expect(useNotesStore.getState().isDirty).toBe(true);
    useNotesStore.getState().applyRemoteUpdate("模型新内容");
    expect(useNotesStore.getState().pendingRemoteUpdate).toBe("模型新内容");
    const before = useNotesStore.getState().items.note_a?.content;
    expect(before).toBe("用户输入中…");
    useNotesStore.getState().acceptPendingRemoteUpdate();
    await vi.runAllTimersAsync();
    expect(useNotesStore.getState().items.note_a?.content).toBe("模型新内容");
    expect(useNotesStore.getState().pendingRemoteUpdate).toBeNull();
  });

  it("applyRemoteUpdate writes immediately when not editing", async () => {
    useNotesStore.getState().applyRemoteUpdate("# 新版\n模型记录");
    expect(useNotesStore.getState().items.note_a?.content).toBe("# 新版\n模型记录");
    expect(useNotesStore.getState().items.note_a?.title).toBe("新版");
    await vi.runAllTimersAsync();
    expect(upsertNote).toHaveBeenCalled();
  });

  it("createNote sets the new note as active and switches to detail view", async () => {
    useNotesStore.getState().setView("list");
    await useNotesStore.getState().createNote();
    expect(useNotesStore.getState().activeId).toBe("note_new");
    expect(useNotesStore.getState().view).toBe("detail");
  });

  it("clearActiveContent only resets content, keeps the note", async () => {
    await useNotesStore.getState().clearActiveContent();
    expect(clearNoteContent).toHaveBeenCalledWith("note_a");
    expect(useNotesStore.getState().items.note_a?.content).toBe("");
    expect(useNotesStore.getState().items.note_a).toBeDefined();
  });

  it("selectNote switches active id and triggers persistence", async () => {
    await useNotesStore.getState().selectNote("note_b");
    expect(setActiveNote).toHaveBeenCalledWith("note_b");
    expect(useNotesStore.getState().activeId).toBe("note_b");
    expect(useNotesStore.getState().view).toBe("detail");
  });
});
