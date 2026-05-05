from __future__ import annotations

import json


def main() -> int:
    import torch
    import torch_geometric

    payload: dict[str, object] = {
        "torch": torch.__version__,
        "torch_cuda": torch.version.cuda,
        "torch_geometric": torch_geometric.__version__,
        "cuda_available": torch.cuda.is_available(),
        "device": None,
        "capability": None,
        "matmul_sum": None,
        "pyg_extensions": {},
    }
    for module_name in ("pyg_lib", "torch_scatter", "torch_sparse", "torch_cluster"):
        try:
            module = __import__(module_name)
            payload["pyg_extensions"][module_name] = getattr(module, "__version__", "ok")
        except Exception as exc:  # noqa: BLE001
            payload["pyg_extensions"][module_name] = f"missing: {exc}"

    if torch.cuda.is_available():
        payload["device"] = torch.cuda.get_device_name(0)
        payload["capability"] = torch.cuda.get_device_capability(0)
        x = torch.randn((512, 512), device="cuda")
        y = x @ x.T
        torch.cuda.synchronize()
        payload["matmul_sum"] = float(y.sum().detach().cpu())

    print(json.dumps(payload, ensure_ascii=False, indent=2))
    if not torch.cuda.is_available():
        raise SystemExit("CUDA is not available in the recommendation training environment")
    return 0
