# SubTag InfoBox Backend Contract

This doc describes the backend contract used by the KnowledgeGraph sub-tag hover InfoBox.

Status: implemented in `Ascend/src/django_api/views.py:KnowledgeTagDetailAPIView`.

## Related Frontend

- UI: `AscendWeb/src/components/KnowledgeGraph/SubTags.tsx`, `AscendWeb/src/components/KnowledgeGraph/InfoBox.tsx`
- API client: `AscendWeb/src/services/api.ts:fetchKnowledgeTagDetail`

## Semantics

- If a knowledge point has no evidence/record for the student, `mastery = 0` (not 0.5).
- For hover details, the backend returns counts + recommendations; the frontend maps them into InfoBox fields.

## Endpoint

`POST /api/knowledge/tag-detail`

### Request

```json
{
  "student": "<full_name>",
  "student_id": "<full_name>",
  "knowledge_point": "数组",
  "top_k": 6,
  "exclude_done": true,
  "include_links": true
}
```

Notes:

- `student` must match the authenticated user's `full_name` (backend enforces this).
- `knowledge_point` must be a canonical name from `Ascend/config/knowledge_tree.yaml`.

### Response

```json
{
  "student": "<full_name>",
  "knowledge_point": "数组",
  "solved": 12,
  "attempted": 18,
  "recommendations": [
    {
      "problem_id": "bank_whrlg_1234",
      "title": "...",
      "difficulty": 3,
      "knowledge_points": ["数组", "双指针"],
      "score": 0.81,
      "reason": "...",
      "url": "/oj/bank_whrlg_1234"
    }
  ]
}
```

If there are no recommendations, `recommendations` is an empty list (`[]`).

## How `solved` / `attempted` Is Computed

To shrink the eventual-consistency window, the endpoint merges two sources:

- Ascend submissions store (parquet base + deltas)
- Local OJ DB (`OjSubmitRecord`) for “already done but not yet ingested” submissions

Counts are deduped by `global_problem_id`.

## URL Resolution

When `include_links=true`, the endpoint returns a SPA route:

- `url = /oj/<problem_id>`
