import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";

import { TitleBar } from "@/components/layout/TitleBar";
import { useLayoutStore } from "@/stores/layoutStore";

describe("TitleBar achievement button", () => {
  beforeEach(() => {
    useLayoutStore.getState().closeFullscreenView();
    (window as unknown as { electronAPI?: unknown }).electronAPI = {
      platform: "linux",
      openFeedbackWindow: async () => {},
      minimize: () => {},
      maximize: () => {},
      close: () => {},
    };
  });

  it("opens achievement fullscreen view", () => {
    render(<TitleBar />);
    fireEvent.click(screen.getByRole("button", { name: "打开成就页面" }));
    expect(useLayoutStore.getState().activeFullscreenView).toBe("achievements");
  });
});
