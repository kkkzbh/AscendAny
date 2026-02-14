from .auth import router as auth_router
from .chat import router as chat_router
from .health import router as health_router
from .import_data import router as import_router
from .meta import router as meta_router
from .model import router as model_router
from .students import router as students_router

__all__ = [
    "auth_router",
    "chat_router",
    "health_router",
    "import_router",
    "meta_router",
    "model_router",
    "students_router",
]
