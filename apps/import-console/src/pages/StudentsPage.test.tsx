import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { IssuedEnrollment, ManagedStudent, ManagedStudentPage } from "@ascendany/sdk";
import { StudentsPage } from "./StudentsPage";

const students: ManagedStudent[] = [
  {
    studentNumber: "20260001",
    pintiaUserId: "pintia-user-1",
    sourceDisplayName: "王同学",
    account: null,
    rating: 1512,
  },
  {
    studentNumber: "20260002",
    pintiaUserId: "pintia-user-2",
    sourceDisplayName: "李同学",
    account: {
      id: "123e4567-e89b-42d3-a456-426614174001",
      username: "student_2",
      displayName: "李同学",
      disabledAt: null,
    },
    rating: 1498,
  },
];

const issuedEnrollment: IssuedEnrollment = {
  grant: {
    id: "223e4567-e89b-42d3-a456-426614174000",
    username: "student_1",
    displayName: "王同学",
    studentNumber: "20260001",
    issuerAccountId: "323e4567-e89b-42d3-a456-426614174000",
    issuedAt: "2026-07-11T01:00:00.000Z",
    expiresAt: "2026-07-12T01:00:00.000Z",
  },
  token: "single-use-enrollment-token",
};

const api = vi.hoisted(() => ({
  getManagedStudents: vi.fn<() => Promise<ManagedStudentPage>>(),
  issueManagedEnrollmentClaim: vi.fn(),
  revokeManagedEnrollmentClaim: vi.fn(),
}));

const clipboardWrite = vi.hoisted(() => vi.fn());

vi.mock("../api/administration", () => api);

async function openIssueForm() {
  fireEvent.click(await screen.findByRole("button", { name: "签发 claim" }));
  fireEvent.change(screen.getByLabelText("用户名"), { target: { value: "student_1" } });
}

async function issueClaim() {
  await openIssueForm();
  fireEvent.click(screen.getByRole("button", { name: "签发一次性 claim" }));
  await screen.findByTestId("enrollment-token");
}

describe("StudentsPage enrollment claims", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    api.getManagedStudents.mockResolvedValue({ items: students, nextCursor: null });
    api.issueManagedEnrollmentClaim.mockResolvedValue(issuedEnrollment);
    api.revokeManagedEnrollmentClaim.mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText: clipboardWrite },
    });
  });

  it("issues from an unbound row with an explicit lifetime and copies only after a click", async () => {
    render(<StudentsPage />);
    await openIssueForm();

    expect(screen.getByLabelText("显示名称")).toHaveValue("王同学");
    expect(screen.getByLabelText("有效期 expiresInSeconds")).toHaveValue(86_400);
    expect(clipboardWrite).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: "签发一次性 claim" }));

    expect(await screen.findByTestId("enrollment-token")).toHaveTextContent(issuedEnrollment.token);
    expect(api.issueManagedEnrollmentClaim).toHaveBeenCalledWith({
      username: "student_1",
      displayName: "王同学",
      studentNumber: "20260001",
      expiresInSeconds: 86_400,
    });
    expect(screen.getByText(issuedEnrollment.grant.id)).toBeInTheDocument();
    expect(screen.getByText("过期时间").parentElement).toHaveTextContent("2026");
    expect(clipboardWrite).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: "复制 token" }));
    await waitFor(() => expect(clipboardWrite).toHaveBeenCalledWith(issuedEnrollment.token));

    fireEvent.click(screen.getByRole("button", { name: "已保存，隐藏 token" }));
    expect(screen.queryByTestId("enrollment-token")).not.toBeInTheDocument();
    expect(screen.getByText("一次性 token 已从当前页面内存清除，无法再次显示。")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "复制 token" })).not.toBeInTheDocument();
  });

  it("never restores a one-time token after the page is remounted", async () => {
    const first = render(<StudentsPage />);
    await issueClaim();
    expect(screen.getByText(issuedEnrollment.token)).toBeInTheDocument();

    first.unmount();
    render(<StudentsPage />);
    await screen.findByText("pintia-user-1");

    expect(screen.queryByText(issuedEnrollment.token)).not.toBeInTheDocument();
    expect(screen.queryByText(issuedEnrollment.grant.id)).not.toBeInTheDocument();
  });

  it("revokes the current grant explicitly and clears its secret", async () => {
    render(<StudentsPage />);
    await issueClaim();

    fireEvent.click(screen.getByRole("button", { name: "撤销并清除" }));

    await waitFor(() => expect(api.revokeManagedEnrollmentClaim).toHaveBeenCalledWith(issuedEnrollment.grant.id));
    expect(screen.queryByText(issuedEnrollment.token)).not.toBeInTheDocument();
    expect(screen.getByText(new RegExp(`Grant ${issuedEnrollment.grant.id} 已撤销`))).toBeInTheDocument();
  });

  it("shows issue conflicts and revoke conflicts as explicit states", async () => {
    api.issueManagedEnrollmentClaim.mockRejectedValueOnce(new Error("已存在有效 enrollment claim（409）"));
    const first = render(<StudentsPage />);
    await openIssueForm();
    fireEvent.click(screen.getByRole("button", { name: "签发一次性 claim" }));
    expect(await screen.findByText("已存在有效 enrollment claim（409）")).toBeInTheDocument();

    first.unmount();
    api.issueManagedEnrollmentClaim.mockResolvedValueOnce(issuedEnrollment);
    api.revokeManagedEnrollmentClaim.mockRejectedValueOnce(new Error("凭据已过期、已消费或已撤销（409）"));
    render(<StudentsPage />);
    await issueClaim();
    fireEvent.click(screen.getByRole("button", { name: "撤销并清除" }));

    expect(await screen.findByText("凭据已过期、已消费或已撤销（409）")).toBeInTheDocument();
    expect(screen.getByText(issuedEnrollment.token)).toBeInTheDocument();
  });

  it("does not allow a bound student row to open an issue form", async () => {
    render(<StudentsPage />);
    const boundAction = await screen.findByRole("button", { name: "已绑定" });

    expect(boundAction).toBeDisabled();
    fireEvent.click(boundAction);
    expect(screen.queryByRole("button", { name: "签发一次性 claim" })).not.toBeInTheDocument();
    expect(api.issueManagedEnrollmentClaim).not.toHaveBeenCalled();
  });
});
