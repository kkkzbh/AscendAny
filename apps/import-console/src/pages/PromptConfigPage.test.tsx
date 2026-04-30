import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { PromptConfigPage } from "./PromptConfigPage";
import type {
  AdminPromptDetail,
  AdminPromptListResponse,
  AdminPromptPreviewResponse,
} from "../api/admin";

const adminMocks = vi.hoisted(() => ({
  getAdminPrompts: vi.fn<() => Promise<AdminPromptListResponse>>(),
  getAdminPrompt: vi.fn<(key: string) => Promise<AdminPromptDetail>>(),
  patchAdminPrompt: vi.fn<(key: string, payload: unknown) => Promise<AdminPromptDetail>>(),
  previewAdminPrompt: vi.fn<(key: string, payload: unknown) => Promise<AdminPromptPreviewResponse>>(),
  restoreAdminPrompt: vi.fn<(key: string, version: number) => Promise<AdminPromptDetail>>(),
}));

vi.mock("../api/admin", () => adminMocks);

const BASE_DETAIL: AdminPromptDetail = {
  key: "chat.normal_system",
  title: "普通聊天系统提示词",
  description: "学生端日常对话的基础系统提示词。",
  category: "chat",
  allowedVariables: ["role_name", "tool_rules"],
  requiredVariables: ["role_name", "tool_rules"],
  version: 1,
  updatedBy: "system",
  updatedAt: "2026-02-01T00:00:00Z",
  content: "你是{role_name}\n{tool_rules}",
  defaultContent: "你是{role_name}\n{tool_rules}",
  sampleVariables: {
    role_name: "小D",
    tool_rules: "工具规则",
  },
  history: [
    {
      versionId: "1",
      version: 1,
      content: "你是{role_name}\n{tool_rules}",
      changeNote: "系统默认版本",
      updatedBy: "system",
      createdAt: "2026-02-01T00:00:00Z",
    },
  ],
};

const BASE_LIST: AdminPromptListResponse = {
  items: [
    {
      key: BASE_DETAIL.key,
      title: BASE_DETAIL.title,
      description: BASE_DETAIL.description,
      category: BASE_DETAIL.category,
      allowedVariables: BASE_DETAIL.allowedVariables,
      requiredVariables: BASE_DETAIL.requiredVariables,
      version: BASE_DETAIL.version,
      updatedBy: BASE_DETAIL.updatedBy,
      updatedAt: BASE_DETAIL.updatedAt,
    },
    {
      key: "role.sakiko.style",
      title: "Sakiko角色风格",
      description: "角色风格",
      category: "role",
      allowedVariables: [],
      requiredVariables: [],
      version: 1,
      updatedBy: "system",
      updatedAt: "2026-02-01T00:00:00Z",
    },
  ],
};

describe("PromptConfigPage", () => {
  beforeEach(() => {
    adminMocks.getAdminPrompts.mockReset();
    adminMocks.getAdminPrompt.mockReset();
    adminMocks.patchAdminPrompt.mockReset();
    adminMocks.previewAdminPrompt.mockReset();
    adminMocks.restoreAdminPrompt.mockReset();
  });

  it("loads, previews, and saves an edited prompt", async () => {
    const savedDetail = {
      ...BASE_DETAIL,
      version: 2,
      content: "新版 {role_name}\n{tool_rules}",
      history: [
        {
          versionId: "2",
          version: 2,
          content: "新版 {role_name}\n{tool_rules}",
          changeNote: "测试保存",
          updatedBy: "admin",
          createdAt: "2026-02-02T00:00:00Z",
        },
        ...BASE_DETAIL.history,
      ],
    };
    adminMocks.getAdminPrompts.mockResolvedValue(BASE_LIST);
    adminMocks.getAdminPrompt.mockResolvedValue(BASE_DETAIL);
    adminMocks.previewAdminPrompt.mockResolvedValue({ rendered: "新版 小D\n工具规则" });
    adminMocks.patchAdminPrompt.mockResolvedValue(savedDetail);

    render(<PromptConfigPage />);

    const editor = await screen.findByLabelText("提示词内容");
    fireEvent.change(editor, { target: { value: "新版 {role_name}\n{tool_rules}" } });
    fireEvent.change(screen.getByLabelText("变更说明"), {
      target: { value: "测试保存" },
    });
    fireEvent.click(screen.getByRole("button", { name: "预览" }));

    expect(await screen.findByText(/新版 小D/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "保存提示词" }));

    await waitFor(() => {
      expect(adminMocks.patchAdminPrompt).toHaveBeenCalledWith("chat.normal_system", {
        content: "新版 {role_name}\n{tool_rules}",
        changeNote: "测试保存",
      });
    });
    expect(await screen.findByText("提示词已保存")).toBeInTheDocument();
  });

  it("restores a historical version", async () => {
    const restoredDetail = {
      ...BASE_DETAIL,
      version: 2,
      history: [
        {
          versionId: "2",
          version: 2,
          content: BASE_DETAIL.content,
          changeNote: "回滚到版本 1",
          updatedBy: "admin",
          createdAt: "2026-02-02T00:00:00Z",
        },
        ...BASE_DETAIL.history,
      ],
    };
    adminMocks.getAdminPrompts.mockResolvedValue(BASE_LIST);
    adminMocks.getAdminPrompt.mockResolvedValue(BASE_DETAIL);
    adminMocks.restoreAdminPrompt.mockResolvedValue(restoredDetail);

    render(<PromptConfigPage />);

    fireEvent.click(await screen.findByRole("button", { name: "回滚" }));

    await waitFor(() => {
      expect(adminMocks.restoreAdminPrompt).toHaveBeenCalledWith("chat.normal_system", 1);
    });
    expect(await screen.findByText("已回滚到历史版本 1")).toBeInTheDocument();
  });
});
