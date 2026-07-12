import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { HelpDrawer } from "./HelpDrawer";

describe("HelpDrawer", () => {
  it("documents the strict v2 upload and Go task flow", () => {
    const onClose = vi.fn();
    render(<HelpDrawer open onClose={onClose} />);

    expect(screen.getByRole("dialog", { name: "Pintia 快照导入指南" })).toHaveTextContent(
      "ascendany.pintia.snapshot.v2",
    );
    expect(screen.getByText(/Go v2 runtime/)).toBeInTheDocument();
    expect(screen.getByText(/Access token 只保存在内存/)).toBeInTheDocument();
    expect(screen.queryByText(/\/api\/v1/)).not.toBeInTheDocument();

    fireEvent.click(screen.getByTitle("关闭"));
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
