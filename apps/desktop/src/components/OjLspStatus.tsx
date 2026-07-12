import { useEffect, useRef, useState } from "react";
import {
  Cpp20LspEditorClient,
  type BrowserSession,
  type LspEditorSnapshot,
} from "@ascendany/sdk";

const initialSnapshot: LspEditorSnapshot = {
  state: "disconnected",
  diagnostics: [],
  error: null,
};

export function OjLspStatus({ session, source }: { session: BrowserSession; source: string }) {
  const [snapshot, setSnapshot] = useState<LspEditorSnapshot>(initialSnapshot);
  const clientRef = useRef<Cpp20LspEditorClient | null>(null);
  const sourceRef = useRef(source);
  sourceRef.current = source;

  useEffect(() => {
    const client = new Cpp20LspEditorClient(session);
    clientRef.current = client;
    const unsubscribe = client.subscribe(setSnapshot);
    void client.connect(sourceRef.current).catch(() => undefined);
    return () => {
      unsubscribe();
      if (clientRef.current === client) clientRef.current = null;
      void client.close().catch((error: unknown) => console.error("Failed to close LSP editor session.", error));
    };
  }, [session]);

  useEffect(() => {
    clientRef.current?.change(source);
  }, [source]);

  return <LspSnapshot snapshot={snapshot} />;
}

function LspSnapshot({ snapshot }: { snapshot: LspEditorSnapshot }) {
  return (
    <aside className={`oj-lsp-status ${snapshot.state}`} aria-label="C++20 语言服务" aria-live="polite">
      <div className="oj-lsp-summary">
        <strong>clangd</strong>
        <span>{stateLabel(snapshot.state)}</span>
        <small>{snapshot.diagnostics.length === 0 ? "无诊断" : `${snapshot.diagnostics.length} 条诊断`}</small>
      </div>
      {snapshot.error === null ? null : <p>{snapshot.error}</p>}
      {snapshot.diagnostics.length === 0 ? null : (
        <ol className="oj-lsp-diagnostics">
          {snapshot.diagnostics.map((diagnostic, index) => (
            <li key={`${diagnostic.range.start.line}:${diagnostic.range.start.character}:${index}`}>
              <span>{diagnostic.range.start.line + 1}:{diagnostic.range.start.character + 1}</span>
              <strong>{diagnostic.message}</strong>
            </li>
          ))}
        </ol>
      )}
    </aside>
  );
}

function stateLabel(state: LspEditorSnapshot["state"]): string {
  switch (state) {
    case "connecting": return "LSP 连接中";
    case "ready": return "LSP 已连接";
    case "disconnected": return "LSP 已断开";
    case "error": return "LSP 连接失败";
  }
}
