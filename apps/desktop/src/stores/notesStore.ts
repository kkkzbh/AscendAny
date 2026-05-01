import { create } from "zustand";

const NOTES_MAX_LENGTH = 32_768;
const NOTES_TITLE_MAX_LENGTH = 120;
const PERSIST_DEBOUNCE_MS = 500;
const STRIP_MD_PATTERNS: Array<[RegExp, string]> = [
  [/^#{1,6}\s+/m, ""],
  [/^>\s+/gm, ""],
  [/^[-*+]\s+/gm, ""],
  [/^\d+\.\s+/gm, ""],
  [/`{1,3}([^`]*)`{1,3}/g, "$1"],
  [/!\[[^\]]*\]\([^)]*\)/g, ""],
  [/\[([^\]]+)\]\([^)]*\)/g, "$1"],
  [/[*_~]+/g, ""],
];

export interface NoteRecord {
  id: string;
  title: string;
  content: string;
  titleIsAuto: boolean;
  createdAt: number;
  updatedAt: number;
}

export interface NotesSnapshot {
  items: NoteRecord[];
  activeNoteId: string;
}

interface NotesElectronApi {
  localStateUpsertNote?: (
    value: { id: string; title: string; content: string; titleIsAuto: boolean },
  ) => Promise<unknown>;
  localStateCreateNote?: () => Promise<unknown>;
  localStateDeleteNote?: (id: string) => Promise<unknown>;
  localStateSetActiveNote?: (id: string) => Promise<boolean>;
  localStateClearNoteContent?: (id: string) => Promise<unknown>;
}

type NotesView = "detail" | "list";

interface NotesState {
  items: Record<string, NoteRecord>;
  order: string[];
  activeId: string | null;
  view: NotesView;
  isEditingContent: boolean;
  isDirty: boolean;
  pendingRemoteUpdate: string | null;

  hydrateFromLocalState: (snapshot: unknown) => void;
  resetForAccount: () => void;
  setView: (view: NotesView) => void;
  setIsEditingContent: (value: boolean) => void;

  selectNote: (id: string) => Promise<void>;
  createNote: () => Promise<NoteRecord | null>;
  deleteNote: (id: string) => Promise<void>;

  setTitle: (title: string) => void;
  setContent: (content: string) => void;
  applyRemoteUpdate: (content: string) => void;
  acceptPendingRemoteUpdate: () => void;
  dismissPendingRemoteUpdate: () => void;
  clearActiveContent: () => Promise<void>;
}

function getElectronNotesApi(): NotesElectronApi | undefined {
  return typeof window === "undefined" ? undefined : (window.electronAPI as NotesElectronApi | undefined);
}

function stripMarkdownInline(value: string): string {
  let text = value;
  for (const [pattern, replacement] of STRIP_MD_PATTERNS) {
    text = text.replace(pattern, replacement);
  }
  return text.trim();
}

export function deriveAutoNoteTitle(content: string): string {
  const text = (content || "").trim();
  if (!text) {
    return "";
  }
  const lines = text.split(/\r?\n/);
  for (const raw of lines) {
    const line = raw.trim();
    if (!line) {
      continue;
    }
    const heading = /^#{1,6}\s+(.+)$/.exec(line);
    if (heading) {
      return stripMarkdownInline(heading[1] ?? "").slice(0, NOTES_TITLE_MAX_LENGTH);
    }
    return stripMarkdownInline(line).slice(0, 30);
  }
  return "";
}

function isNoteRecord(value: unknown): value is NoteRecord {
  if (!value || typeof value !== "object") {
    return false;
  }
  const candidate = value as Partial<NoteRecord>;
  return (
    typeof candidate.id === "string"
    && typeof candidate.title === "string"
    && typeof candidate.content === "string"
    && typeof candidate.createdAt === "number"
    && typeof candidate.updatedAt === "number"
    && typeof candidate.titleIsAuto === "boolean"
  );
}

function buildOrder(items: Record<string, NoteRecord>): string[] {
  return Object.values(items)
    .sort((a, b) => b.updatedAt - a.updatedAt || b.createdAt - a.createdAt)
    .map((note) => note.id);
}

function clampContent(value: string): string {
  return value.length <= NOTES_MAX_LENGTH ? value : value.slice(0, NOTES_MAX_LENGTH);
}

function clampTitle(value: string): string {
  return value.slice(0, NOTES_TITLE_MAX_LENGTH);
}

let persistTimer: ReturnType<typeof setTimeout> | null = null;

function schedulePersist(record: NoteRecord): void {
  if (persistTimer) {
    clearTimeout(persistTimer);
  }
  persistTimer = setTimeout(() => {
    persistTimer = null;
    void persistNote(record);
  }, PERSIST_DEBOUNCE_MS);
}

async function persistNote(record: NoteRecord): Promise<void> {
  const api = getElectronNotesApi();
  if (!api?.localStateUpsertNote) {
    return;
  }
  try {
    const result = await api.localStateUpsertNote({
      id: record.id,
      title: record.title,
      content: record.content,
      titleIsAuto: record.titleIsAuto,
    });
    if (isNoteRecord(result)) {
      useNotesStore.setState((state) => ({
        items: { ...state.items, [result.id]: result },
        order: buildOrder({ ...state.items, [result.id]: result }),
      }));
    }
  } catch (error) {
    console.warn("[notes] failed to persist note", error);
  }
}

function flushPendingPersist(): void {
  if (persistTimer) {
    clearTimeout(persistTimer);
    persistTimer = null;
  }
}

export const useNotesStore = create<NotesState>()((set, get) => ({
  items: {},
  order: [],
  activeId: null,
  view: "detail",
  isEditingContent: false,
  isDirty: false,
  pendingRemoteUpdate: null,

  hydrateFromLocalState: (snapshot) => {
    flushPendingPersist();
    const incoming = (snapshot ?? null) as NotesSnapshot | null;
    if (!incoming || !Array.isArray(incoming.items)) {
      set({
        items: {},
        order: [],
        activeId: null,
        isEditingContent: false,
        isDirty: false,
        pendingRemoteUpdate: null,
      });
      return;
    }
    const items: Record<string, NoteRecord> = {};
    for (const note of incoming.items) {
      if (isNoteRecord(note)) {
        items[note.id] = note;
      }
    }
    const order = buildOrder(items);
    const fallback = order[0] ?? null;
    const activeId =
      incoming.activeNoteId && items[incoming.activeNoteId]
        ? incoming.activeNoteId
        : fallback;
    set({
      items,
      order,
      activeId,
      isEditingContent: false,
      isDirty: false,
      pendingRemoteUpdate: null,
    });
  },

  resetForAccount: () => {
    flushPendingPersist();
    set({
      items: {},
      order: [],
      activeId: null,
      view: "detail",
      isEditingContent: false,
      isDirty: false,
      pendingRemoteUpdate: null,
    });
  },

  setView: (view) => {
    set({ view });
  },

  setIsEditingContent: (value) => {
    set({ isEditingContent: value });
    if (!value) {
      set({ isDirty: false });
    }
  },

  selectNote: async (id) => {
    const state = get();
    if (!state.items[id] || state.activeId === id) {
      set({ view: "detail" });
      return;
    }
    flushPendingPersist();
    set({
      activeId: id,
      view: "detail",
      isEditingContent: false,
      isDirty: false,
      pendingRemoteUpdate: null,
    });
    const api = getElectronNotesApi();
    if (api?.localStateSetActiveNote) {
      try {
        await api.localStateSetActiveNote(id);
      } catch (error) {
        console.warn("[notes] failed to set active note", error);
      }
    }
  },

  createNote: async () => {
    flushPendingPersist();
    const api = getElectronNotesApi();
    if (!api?.localStateCreateNote) {
      return null;
    }
    try {
      const created = await api.localStateCreateNote();
      if (!isNoteRecord(created)) {
        return null;
      }
      set((state) => {
        const items = { ...state.items, [created.id]: created };
        return {
          items,
          order: buildOrder(items),
          activeId: created.id,
          view: "detail",
          isEditingContent: false,
          isDirty: false,
          pendingRemoteUpdate: null,
        };
      });
      return created;
    } catch (error) {
      console.warn("[notes] failed to create note", error);
      return null;
    }
  },

  deleteNote: async (id) => {
    flushPendingPersist();
    const api = getElectronNotesApi();
    if (!api?.localStateDeleteNote) {
      return;
    }
    try {
      const result = await api.localStateDeleteNote(id);
      const nextActiveId =
        result && typeof result === "object" && "activeNoteId" in result
          ? String((result as { activeNoteId: unknown }).activeNoteId)
          : null;
      set((state) => {
        const items = { ...state.items };
        delete items[id];
        let nextItems = items;
        if (nextActiveId && !items[nextActiveId]) {
          // Service may have created a replacement note; refresh by hydrating.
          nextItems = items;
        }
        return {
          items: nextItems,
          order: buildOrder(nextItems),
          activeId: nextActiveId ?? state.activeId,
          isEditingContent: false,
          isDirty: false,
          pendingRemoteUpdate: null,
        };
      });
    } catch (error) {
      console.warn("[notes] failed to delete note", error);
    }
  },

  setTitle: (title) => {
    const state = get();
    if (!state.activeId) {
      return;
    }
    const active = state.items[state.activeId];
    if (!active) {
      return;
    }
    const trimmed = clampTitle(title);
    const next: NoteRecord = {
      ...active,
      title: trimmed,
      titleIsAuto: false,
      updatedAt: Date.now(),
    };
    set((current) => ({
      items: { ...current.items, [next.id]: next },
      order: buildOrder({ ...current.items, [next.id]: next }),
    }));
    schedulePersist(next);
  },

  setContent: (content) => {
    const state = get();
    if (!state.activeId) {
      return;
    }
    const active = state.items[state.activeId];
    if (!active) {
      return;
    }
    const safe = clampContent(content);
    const titleIsAuto = active.titleIsAuto;
    const nextTitle = titleIsAuto
      ? deriveAutoNoteTitle(safe)
      : active.title;
    const next: NoteRecord = {
      ...active,
      content: safe,
      title: nextTitle,
      updatedAt: Date.now(),
    };
    set((current) => ({
      items: { ...current.items, [next.id]: next },
      order: buildOrder({ ...current.items, [next.id]: next }),
      isDirty: state.isEditingContent,
    }));
    schedulePersist(next);
  },

  applyRemoteUpdate: (content) => {
    const state = get();
    if (!state.activeId) {
      return;
    }
    const active = state.items[state.activeId];
    if (!active) {
      return;
    }
    const safe = clampContent(content);
    if (state.isEditingContent && state.isDirty) {
      set({ pendingRemoteUpdate: safe });
      return;
    }
    flushPendingPersist();
    const titleIsAuto = active.titleIsAuto;
    const nextTitle = titleIsAuto ? deriveAutoNoteTitle(safe) : active.title;
    const next: NoteRecord = {
      ...active,
      content: safe,
      title: nextTitle,
      updatedAt: Date.now(),
    };
    set((current) => ({
      items: { ...current.items, [next.id]: next },
      order: buildOrder({ ...current.items, [next.id]: next }),
      pendingRemoteUpdate: null,
      isDirty: false,
    }));
    void persistNote(next);
  },

  acceptPendingRemoteUpdate: () => {
    const state = get();
    const pending = state.pendingRemoteUpdate;
    if (pending === null || !state.activeId) {
      return;
    }
    const active = state.items[state.activeId];
    if (!active) {
      return;
    }
    flushPendingPersist();
    const safe = clampContent(pending);
    const titleIsAuto = active.titleIsAuto;
    const nextTitle = titleIsAuto ? deriveAutoNoteTitle(safe) : active.title;
    const next: NoteRecord = {
      ...active,
      content: safe,
      title: nextTitle,
      updatedAt: Date.now(),
    };
    set((current) => ({
      items: { ...current.items, [next.id]: next },
      order: buildOrder({ ...current.items, [next.id]: next }),
      pendingRemoteUpdate: null,
      isDirty: false,
      isEditingContent: false,
    }));
    void persistNote(next);
  },

  dismissPendingRemoteUpdate: () => {
    set({ pendingRemoteUpdate: null });
  },

  clearActiveContent: async () => {
    const state = get();
    if (!state.activeId) {
      return;
    }
    flushPendingPersist();
    const api = getElectronNotesApi();
    if (!api?.localStateClearNoteContent) {
      return;
    }
    try {
      const result = await api.localStateClearNoteContent(state.activeId);
      if (isNoteRecord(result)) {
        set((current) => ({
          items: { ...current.items, [result.id]: result },
          order: buildOrder({ ...current.items, [result.id]: result }),
          isDirty: false,
          pendingRemoteUpdate: null,
        }));
      }
    } catch (error) {
      console.warn("[notes] failed to clear note content", error);
    }
  },
}));

export function selectActiveNote(state: NotesState): NoteRecord | null {
  return state.activeId ? state.items[state.activeId] ?? null : null;
}

export function selectNotesList(state: NotesState): NoteRecord[] {
  return state.order.map((id) => state.items[id]).filter((note): note is NoteRecord => Boolean(note));
}

export const NOTES_LIMITS = {
  CONTENT_MAX_LENGTH: NOTES_MAX_LENGTH,
  TITLE_MAX_LENGTH: NOTES_TITLE_MAX_LENGTH,
  PERSIST_DEBOUNCE_MS,
};
