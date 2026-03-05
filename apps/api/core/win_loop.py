from __future__ import annotations

import asyncio
import sys


def selector_loop_factory() -> asyncio.AbstractEventLoop:
    """Force Selector-based loop creation on Windows.

    Note: uvicorn accepts a custom loop factory as an import string.
    When provided, it will call this function directly to create a new event loop.
    """

    if sys.platform == "win32":
        policy = getattr(asyncio, "WindowsSelectorEventLoopPolicy", None)
        if policy is not None:
            try:
                asyncio.set_event_loop_policy(policy())
            except Exception:
                pass
    return asyncio.new_event_loop()
