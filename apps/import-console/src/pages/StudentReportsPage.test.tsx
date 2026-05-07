import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it, vi, beforeEach } from "vitest";
import { StudentReportsPage } from "./StudentReportsPage";
import type { AdminStudentExamReportsResponse, AdminStudentListResponse } from "../api/admin";

const adminMocks = vi.hoisted(() => ({
  listAdminStudents: vi.fn<() => Promise<AdminStudentListResponse>>(),
  getAdminStudentExamReports: vi.fn<() => Promise<AdminStudentExamReportsResponse>>(),
}));

const examMocks = vi.hoisted(() => ({
  generateExamAnalysis: vi.fn<() => Promise<{ runId: string; message: string }>>(),
}));

vi.mock("../api/admin", () => adminMocks);
vi.mock("../api/examAnalysis", () => examMocks);

const STUDENTS: AdminStudentListResponse = {
  total: 1,
  items: [
    {
      studentEntityId: "101",
      studentId: "20230001",
      studentName: "Alice",
      grade: "2023",
      username: "alice",
      rating: 1002,
      knowledge: 90,
      accuracy: 88,
      quality: 80,
      flexibility: 76,
      proficiency: 79,
      latestExamAt: "2026-02-11T00:00:00+00:00",
      examCount: 2,
      generatedReports: 1,
      failedReports: 0,
      missingReports: 1,
      reportCompletionRate: 0.5,
    },
  ],
};

const REPORTS: AdminStudentExamReportsResponse = {
  student: STUDENTS.items[0]!,
  items: [
    {
      examId: "11",
      examName: "Contest 11",
      examType: "datastructure",
      examDate: "2026-02-11T00:00:00+00:00",
      rank: 1,
      totalScore: 100,
      solvedCount: 4,
      ratingDelta: 22,
      oldRating: 980,
      newRating: 1002,
      knowledge: 90,
      accuracy: 88,
      quality: 80,
      flexibility: 76,
      proficiency: 79,
      analysisStatus: "success",
      analysisReply: "## 分析\n\nAlice report",
      generatedAt: "2026-02-11T00:00:00+00:00",
      errorMessage: null,
    },
  ],
};

describe("StudentReportsPage", () => {
  beforeEach(() => {
    adminMocks.listAdminStudents.mockReset();
    adminMocks.getAdminStudentExamReports.mockReset();
    examMocks.generateExamAnalysis.mockReset();
  });

  it("loads students and renders selected exam markdown report", async () => {
    adminMocks.listAdminStudents.mockResolvedValue(STUDENTS);
    adminMocks.getAdminStudentExamReports.mockResolvedValue(REPORTS);

    render(<StudentReportsPage />);

    expect(await screen.findByText("Alice")).toBeInTheDocument();
    const table = await screen.findByRole("table");
    expect(await within(table).findByText("Contest 11")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "分析" })).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText("搜索学生"), {
      target: { value: "20230001" },
    });

    await waitFor(() => {
      expect(adminMocks.listAdminStudents).toHaveBeenCalled();
    });
  });
});
