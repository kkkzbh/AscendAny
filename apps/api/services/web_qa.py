from __future__ import annotations

import html
import re
from typing import Any

from psycopg.rows import dict_row

from ..schemas.chat import ChatMessageRequest, ChatReplyRequest


def _markdown_to_html(text: str) -> str:
    try:
        import markdown  # type: ignore

        return markdown.markdown(text or "", extensions=["fenced_code", "tables", "nl2br"])
    except Exception:
        escaped = html.escape(text or "")
        escaped = re.sub(r"\*\*(.*?)\*\*", r"<strong>\1</strong>", escaped)
        return "<p>" + escaped.replace("\n\n", "</p><p>").replace("\n", "<br>") + "</p>"


class WebQaService:
    def __init__(self, repository: Any, llm_service: Any) -> None:
        self._repository = repository
        self._llm_service = llm_service

    async def search(self, payload: dict[str, Any]) -> dict[str, Any]:
        query = str(payload.get("query") or "").strip()
        if not query:
            return {"detail": "请输入问题"}
        top_k = max(1, min(int(payload.get("top_k") or 4), 10))
        sources = await self._search_sources(query, top_k=top_k)
        context = "\n\n".join(
            f"[{idx}] 题号: {src['problem_id']}\n标题: {src.get('title') or ''}\n标签: {', '.join(src.get('tags') or [])}\n摘要: {src.get('snippet') or ''}"
            for idx, src in enumerate(sources, start=1)
        )
        system_prompt = (
            "你是数据结构课程答疑助手。只能基于给定题库和通用数据结构知识作答；"
            "如果题库上下文不足，要明确说明。回答使用中文，尽量给出解题思路而不是直接堆代码。"
        )
        user_content = f"问题：{query}\n\n题库检索上下文：\n{context or '无匹配题库上下文'}"
        try:
            reply = await self._llm_service.generate_reply(
                ChatReplyRequest(
                    messages=[ChatMessageRequest(role="user", content=user_content)]
                ),
                system_prompt=system_prompt,
            )
            answer = reply.reply
            model = reply.model
        except Exception as exc:
            answer = f"模型服务暂不可用：{exc}"
            model = ""
        return {
            "query": query,
            "answer_markdown": answer,
            "answer_html": _markdown_to_html(answer),
            "sources": sources,
            "model": model,
        }

    async def _search_sources(self, query: str, *, top_k: int) -> list[dict[str, Any]]:
        like = f"%{query}%"
        rows = await self._fetch_all(
            """
            SELECT pb.problem_id, pb.title, pb.description, pb.link,
                   COALESCE(jsonb_agg(t.knowledge_point ORDER BY t.knowledge_point)
                       FILTER (WHERE t.knowledge_point IS NOT NULL), '[]'::jsonb) AS tags
            FROM ascendany.recommendation_problem_bank pb
            LEFT JOIN ascendany.recommendation_problem_tags t ON t.problem_id = pb.problem_id
            WHERE pb.active = TRUE
              AND (pb.problem_id ILIKE %s OR pb.title ILIKE %s OR pb.description ILIKE %s)
            GROUP BY pb.problem_id
            ORDER BY pb.problem_id
            LIMIT %s
            """,
            (like, like, like, top_k),
        )
        return [
            {
                "problem_id": str(row["problem_id"]),
                "title": row.get("title") or str(row["problem_id"]),
                "tags": row.get("tags") if isinstance(row.get("tags"), list) else [],
                "url": f"/oj/{row['problem_id']}",
                "snippet": self._snippet(str(row.get("description") or ""), query),
            }
            for row in rows
        ]

    @staticmethod
    def _snippet(text: str, query: str) -> str:
        plain = re.sub(r"<[^>]+>", "", text or "")
        plain = re.sub(r"\s+", " ", plain).strip()
        idx = plain.lower().find(query.lower())
        if idx < 0:
            return plain[:180]
        start = max(0, idx - 60)
        return plain[start : start + 220]

    async def _fetch_all(self, query: str, params: tuple[Any, ...]) -> list[dict[str, Any]]:
        async with self._repository._pool.connection() as conn:
            async with conn.cursor(row_factory=dict_row) as cursor:
                await cursor.execute(query, params)
                rows = await cursor.fetchall()
        return [dict(row) for row in rows]
