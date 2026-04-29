import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { LocalStateService } from "../electron/localState";

describe("LocalStateService", () => {
  let dir: string;
  let dbPath: string;
  let service: LocalStateService;

  beforeEach(() => {
    dir = mkdtempSync(join(tmpdir(), "ascendany-local-state-"));
    dbPath = join(dir, "state_v2.sqlite");
    service = new LocalStateService(dbPath);
  });

  afterEach(() => {
    service.close();
    rmSync(dir, { recursive: true, force: true });
  });

  it("creates a default profile for an empty database", () => {
    const snapshot = service.hydrate();

    expect(snapshot.profile.id).toMatch(/^profile_/);
    expect(snapshot.profile.displayName).toBe("本地资料");
    expect(snapshot.settings.useOpaqueSidebarBackground).toBe(true);
    expect(snapshot.chat.sessions).toHaveLength(1);
    expect(snapshot.chat.activeSessionId).toBe(snapshot.chat.sessions[0].id);
  });

  it("persists settings, layout, and chat across service restarts", () => {
    service.saveSettings({
      theme: "dark",
      useOpaqueSidebarBackground: false,
      zoomPercent: 118,
      activeRole: "custom_role",
    });
    service.saveLayout({
      isLeftSidebarCollapsed: true,
      leftSidebarRatio: 0.24,
      isMetricsPanelVisible: false,
      activeRightPanelTab: "history",
      splitRatio: 0.37,
      activeFullscreenView: "achievements",
    });
    service.saveChat({
      activeSessionId: "session_1",
      sessions: [
        {
          id: "session_1",
          title: "能力分析",
          summary: "summary",
          createdAt: 1000,
          updatedAt: 2000,
          messages: [
            {
              id: "msg_1",
              role: "user",
              content: "分析最近一次考试",
              timestamp: 1100,
            },
          ],
        },
      ],
    });

    service.close();
    service = new LocalStateService(dbPath);
    const snapshot = service.hydrate();

    expect(snapshot.settings.theme).toBe("dark");
    expect(snapshot.settings.useOpaqueSidebarBackground).toBe(false);
    expect(snapshot.settings.zoomPercent).toBe(120);
    expect(snapshot.layout.activeRightPanelTab).toBe("history");
    expect(snapshot.layout.activeFullscreenView).toBe("achievements");
    expect(snapshot.chat.activeSessionId).toBe("session_1");
    expect(snapshot.chat.sessions[0].messages[0].content).toBe("分析最近一次考试");
  });

  it("binds login metadata without replacing local chat or settings", () => {
    service.saveSettings({ theme: "dark", useOpaqueSidebarBackground: false });
    service.saveChat({
      activeSessionId: "session_keep",
      sessions: [
        {
          id: "session_keep",
          title: "保留",
          summary: "",
          createdAt: 1000,
          updatedAt: 1000,
          messages: [],
        },
      ],
    });

    const profile = service.bindActiveProfile({
      accountId: "acc-1",
      username: "alice",
      displayName: "Alice",
    });
    const snapshot = service.hydrate();

    expect(profile?.accountId).toBe("acc-1");
    expect(snapshot.profile.accountId).toBe("acc-1");
    expect(snapshot.settings.theme).toBe("dark");
    expect(snapshot.chat.activeSessionId).toBe("session_keep");
  });
});
