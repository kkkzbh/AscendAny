from __future__ import annotations

from datetime import datetime

from pydantic import BaseModel


class LatestExamImportedAtResponse(BaseModel):
    latestExamImportedAt: datetime | None
