from .chat import router as chat_router
from .health import router as health_router
from .meta import router as meta_router
from .model import router as model_router
from .students import router as students_router

__all__ = [
    "chat_router",
    "health_router",
    "meta_router",
    "model_router",
    "students_router",
]
