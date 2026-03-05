import os
import sys

import uvicorn


def main() -> int:
    host = os.getenv("ASCENDANY_API_HOST", "127.0.0.1")
    port = int(os.getenv("ASCENDANY_API_PORT", "8000"))

    loop = "auto"
    if sys.platform == "win32":
        loop = "apps.api.core.win_loop:selector_loop_factory"

    config = uvicorn.Config(
        "apps.api.main:app",
        host=host,
        port=port,
        log_level=os.getenv("ASCENDANY_API_LOG_LEVEL", "info"),
        loop=loop,
        reload=False,
    )
    server = uvicorn.Server(config)
    server.run()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
