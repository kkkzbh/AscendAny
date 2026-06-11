# AscendAny

<p align="center">
  <img src="image/LOGO_SHRIND.png" alt="AscendAny Logo" width="96" />
</p>

<p align="center">
  <strong>Student Ability Analytics Platform</strong>
</p>

<p align="center">
  Turn programming-exam data into explainable growth insights, helping students understand recent performance and helping teachers quickly identify teaching priorities.
</p>

![AscendAny main interface](image/主界面.png)

## Project Overview

AscendAny is designed for programming courses, training camps, and class exams. The platform continuously ingests exam data, automatically builds student profiles, and uses an AI assistant to explain recent performance, ability changes, and training priorities.

It focuses on three core questions:

- How did a student perform in the latest exam, and are the ranking, score, solved count, and historical changes clear?
- How are the five ability dimensions changing, and can strengths and weaknesses be seen at a glance?
- What should the student practice next, and are the suggestions directly usable for review and study?

## Core Capabilities

### Incremental Exam Import

Each exam directory can be imported as an independent exam. The platform detects new exams and new snapshots, preserves historical data, and avoids re-importing the same records.

### Five-Dimensional Ability Profile

The platform decomposes exam performance into five dimensions:

| Dimension | Meaning |
| --- | --- |
| Knowledge | Reflects mastery of knowledge points, combining pass rate and score performance. |
| Accuracy | Focuses on submissions before AC, measuring solving stability and trial-and-error cost. |
| Quality | Combines runtime and related data to describe solution quality. |
| Flexibility | Observes problem-switching rhythm and stuck time, reflecting in-contest strategy adjustment. |
| Proficiency | Focuses on solving speed and completion rhythm, showing training fluency. |

### Rating Tracking

Each exam updates the overall rating and records the change for that exam. Students can see their growth trend, and teachers can use it to observe class-wide changes.

### AI-Powered Interpretation

The AI assistant can combine exam records, ability indicators, and rating history to generate recent performance analysis, weakness explanations, problem recommendations, and training suggestions. Students can continue asking about a specific exam, a specific metric, or the next study plan.

## Interface Preview

### Login and Account Binding

After registration, students bind their student ID and platform nickname. The system associates the account with exam data and opens the personal learning workspace.

![Login screen](image/登录界面.png)

### Learning Workspace

The left side keeps history conversations and entry points, the center displays AI analysis, problem recommendations, and learning-path reminders, and the right side gathers ability, history, map, and note panels.

![Main interface overview](image/主界面.png)

### Ability Panel

The ability panel shows the overall rating, a five-dimensional radar chart, and individual ability scores. Students can quickly identify current strengths, weaknesses, and changes caused by the latest exam.

![Ability panel](image/能力面板.png)

### AI Role Switching

The platform supports switching AI teaching-assistant roles and also supports locally customized roles, using different tones and analysis styles to accompany review.

![Role switching](image/角色切换.png)

## From Exam Data to Study Suggestions

1. Put in new exam data.
2. The platform scans new exams and snapshots.
3. Standardized data enters the student ability calculation pipeline.
4. The system generates five-dimensional ability scores, rating changes, and historical trends.
5. The AI assistant provides actionable study suggestions based on the latest data.

## Who Is It For

- Students: view recent exam performance, ability weaknesses, and the next training focus.
- Teachers and teaching assistants: observe class learning status and locate common issues and individual differences.
- Administrators: maintain the exam data import pipeline and track import progress and data quality.

## Platform Components

- Student side: desktop client, web access, and Android app.
- Admin side: import console for starting data imports and viewing task progress.
- Backend service: provides authentication, student profiles, exam analysis, AI chat, and import-task APIs.
- Data preprocessing: scans, parses, imports, and computes ability indicators from exam data.

## Project Entry Points

| Module | Description |
| --- | --- |
| `apps/desktop/` | Student desktop client and desktop web build entry point. |
| `apps/mobile/` | Android app entry point. |
| `apps/import-console/` | Administrator import console. |
| `apps/api/` | FastAPI backend service. |
| `preprocess/` | Exam data preprocessing and incremental import. |
| `doc/` | Data specifications, ability model, platform architecture, and deployment documentation. |

For more development, deployment, and data-specification notes, see the [documentation index](doc/文档索引.md).
