import { useNotesStore } from "@/stores/notesStore";
import { NotesDetailView } from "./NotesDetailView";
import { NotesListView } from "./NotesListView";

export function NotesWorkspace() {
  const view = useNotesStore((state) => state.view);
  return (
    <div className="notes-workspace">
      {view === "list" ? <NotesListView /> : <NotesDetailView />}
    </div>
  );
}
