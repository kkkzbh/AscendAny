from __future__ import annotations

from dataclasses import dataclass


@dataclass(slots=True)
class AppError(Exception):
    status_code: int
    code: str
    message: str

    def to_dict(self) -> dict[str, dict[str, str]]:
        return {
            "error": {
                "code": self.code,
                "message": self.message,
            }
        }
