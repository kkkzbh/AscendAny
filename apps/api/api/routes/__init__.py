from .admin import router as admin_router
from .auth import router as auth_router
from .chat import router as chat_router
from .exam_analysis import router as exam_analysis_router
from .health import router as health_router
from .import_data import router as import_router
from .meta import router as meta_router
from .students import router as students_router

__all__ = [
    "admin_router",
    "auth_router",
    "chat_router",
    "exam_analysis_router",
    "health_router",
    "import_router",
    "meta_router",
    "students_router",
]
