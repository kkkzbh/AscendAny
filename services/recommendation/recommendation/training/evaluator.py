"""
评估器

计算推荐系统评估指标。
"""
from typing import Dict, List, Optional
import torch


def hit_rate_at_k(
    predictions: torch.Tensor,
    ground_truth: torch.Tensor,
    k: int = 10,
) -> float:
    """计算 Hit Rate @K

    Args:
        predictions: 预测分数 [num_users, num_items]
        ground_truth: 真实标签 [num_users, num_items] (1=正样本)
        k: Top-K

    Returns:
        命中率
    """
    if predictions.numel() == 0 or ground_truth.numel() == 0:
        return 0.0
    k = min(k, predictions.size(1))
    if k <= 0:
        return 0.0
    _, top_k_indices = torch.topk(predictions, k, dim=1)
    hits = torch.gather(ground_truth, 1, top_k_indices).sum(dim=1) > 0
    return hits.float().mean().item()


def ndcg_at_k(
    predictions: torch.Tensor,
    ground_truth: torch.Tensor,
    k: int = 10,
) -> float:
    """计算 NDCG @K

    Args:
        predictions: 预测分数 [num_users, num_items]
        ground_truth: 真实标签 [num_users, num_items] (1=正样本)
        k: Top-K

    Returns:
        NDCG 值
    """
    if predictions.numel() == 0 or ground_truth.numel() == 0:
        return 0.0
    k = min(k, predictions.size(1))
    if k <= 0:
        return 0.0
    _, top_k_indices = torch.topk(predictions, k, dim=1)
    relevance = torch.gather(ground_truth, 1, top_k_indices)

    # DCG
    positions = torch.arange(1, k + 1, device=predictions.device).float()
    discounts = torch.log2(positions + 1)
    dcg = (relevance / discounts).sum(dim=1)

    # IDCG (理想情况下的 DCG)
    ideal_relevance = torch.sort(ground_truth, dim=1, descending=True)[0][:, :k]
    idcg = (ideal_relevance / discounts).sum(dim=1)

    # 避免除以零
    ndcg = dcg / (idcg + 1e-10)
    return ndcg.mean().item()


def precision_at_k(
    predictions: torch.Tensor,
    ground_truth: torch.Tensor,
    k: int = 10,
) -> float:
    """计算 Precision @K

    Args:
        predictions: 预测分数 [num_users, num_items]
        ground_truth: 真实标签 [num_users, num_items] (1=正样本)
        k: Top-K

    Returns:
        Precision 值
    """
    if predictions.numel() == 0 or ground_truth.numel() == 0:
        return 0.0
    k = min(k, predictions.size(1))
    if k <= 0:
        return 0.0
    _, top_k_indices = torch.topk(predictions, k, dim=1)
    relevance = torch.gather(ground_truth, 1, top_k_indices)
    hits = relevance.sum(dim=1)
    return (hits / k).mean().item()


def recall_at_k(
    predictions: torch.Tensor,
    ground_truth: torch.Tensor,
    k: int = 10,
) -> float:
    """计算 Recall @K

    Args:
        predictions: 预测分数 [num_users, num_items]
        ground_truth: 真实标签 [num_users, num_items] (1=正样本)
        k: Top-K

    Returns:
        Recall 值
    """
    if predictions.numel() == 0 or ground_truth.numel() == 0:
        return 0.0
    k = min(k, predictions.size(1))
    if k <= 0:
        return 0.0
    _, top_k_indices = torch.topk(predictions, k, dim=1)
    relevance = torch.gather(ground_truth, 1, top_k_indices)
    hits = relevance.sum(dim=1)
    num_pos = ground_truth.sum(dim=1)
    recall = torch.where(num_pos > 0, hits / num_pos, torch.zeros_like(hits))
    return recall.mean().item()


def mrr_at_k(
    predictions: torch.Tensor,
    ground_truth: torch.Tensor,
    k: int = 10,
) -> float:
    """计算 MRR @K（Mean Reciprocal Rank）

    对每个用户，取 Top-K 推荐列表中第一个命中正样本的位置 rank（从 1 开始），该用户得分为 1/rank；
    若 Top-K 无命中则为 0。最后对所有用户取平均。

    Args:
        predictions: 预测分数 [num_users, num_items]
        ground_truth: 真实标签 [num_users, num_items] (1=正样本)
        k: Top-K

    Returns:
        MRR 值
    """
    if predictions.numel() == 0 or ground_truth.numel() == 0:
        return 0.0
    k = min(k, predictions.size(1))
    if k <= 0:
        return 0.0
    _, top_k_indices = torch.topk(predictions, k, dim=1)
    relevance = torch.gather(ground_truth, 1, top_k_indices)  # [N, K]

    positions = (
        torch.arange(1, k + 1, device=predictions.device, dtype=torch.float32)
        .unsqueeze(0)
        .expand(relevance.size(0), -1)
    )
    inf = torch.full_like(positions, float("inf"))
    first_rank = torch.where(relevance > 0, positions, inf).min(dim=1).values
    rr = torch.where(torch.isfinite(first_rank), 1.0 / first_rank, torch.zeros_like(first_rank))
    return rr.mean().item()


def map_at_k(
    predictions: torch.Tensor,
    ground_truth: torch.Tensor,
    k: int = 10,
) -> float:
    """计算 MAP @K（Mean Average Precision）

    对每个用户计算 AP@K：
      AP@K = sum_{i=1..K} Precision@i * rel_i / min(num_pos, K)
    其中 rel_i 表示 Top-i 的第 i 个推荐是否为正样本，num_pos 是该用户的正样本总数。
    最后对所有用户取平均得到 MAP@K。

    Args:
        predictions: 预测分数 [num_users, num_items]
        ground_truth: 真实标签 [num_users, num_items] (1=正样本)
        k: Top-K

    Returns:
        MAP 值
    """
    if predictions.numel() == 0 or ground_truth.numel() == 0:
        return 0.0
    k = min(k, predictions.size(1))
    if k <= 0:
        return 0.0
    _, top_k_indices = torch.topk(predictions, k, dim=1)
    relevance = torch.gather(ground_truth, 1, top_k_indices)  # [N, K]

    positions = torch.arange(1, k + 1, device=predictions.device, dtype=torch.float32)
    cum_hits = relevance.cumsum(dim=1)
    precision_at_i = cum_hits / positions.unsqueeze(0)

    ap_num = (precision_at_i * relevance).sum(dim=1)
    num_pos = ground_truth.sum(dim=1).clamp(min=0.0)
    denom = torch.clamp(num_pos, max=float(k))
    ap = torch.where(denom > 0, ap_num / denom, torch.zeros_like(ap_num))
    return ap.mean().item()


def _build_edge_map(
    edge_index: Optional[torch.Tensor],
    num_students: int,
) -> List[set]:
    """构建学生 -> 题目集合的映射"""
    mapping = [set() for _ in range(num_students)]
    if edge_index is None or edge_index.numel() == 0:
        return mapping

    # 允许传入 CPU/GPU 张量
    for student_idx, problem_idx in edge_index.t().tolist():
        if 0 <= student_idx < num_students:
            mapping[student_idx].add(problem_idx)
    return mapping


class Evaluator:
    """推荐评估器

    评估推荐模型的性能。
    """

    def __init__(self, k_values: List[int] = [5, 10, 20], batch_size: int = 1024):
        """初始化评估器

        Args:
            k_values: 要评估的 K 值列表
            batch_size: 评估批次大小（按学生分批）
        """
        self.k_values = k_values
        self.batch_size = batch_size

    def evaluate(
        self,
        model,
        data,
        test_edges: torch.Tensor,
        num_students: int,
        num_problems: int,
        exclude_edges: Optional[torch.Tensor] = None,
        batch_size: Optional[int] = None,
        ignore_empty_students: bool = False,
    ) -> Dict[str, float]:
        """评估模型

        Args:
            model: 推荐模型
            data: HeteroData
            test_edges: 测试集边 [2, num_edges]
            num_students: 学生数量
            num_problems: 题目数量
            exclude_edges: 需要排除的已知边（如训练/验证集）
            batch_size: 评估批次大小（按学生分批）
            ignore_empty_students: 是否忽略测试集中无正样本的学生

        Returns:
            评估指标字典
        """
        model.eval()

        if num_students == 0 or num_problems == 0:
            return (
                {f"hit_rate@{k}": 0.0 for k in self.k_values}
                | {f"ndcg@{k}": 0.0 for k in self.k_values}
                | {f"precision@{k}": 0.0 for k in self.k_values}
                | {f"recall@{k}": 0.0 for k in self.k_values}
                | {f"mrr@{k}": 0.0 for k in self.k_values}
                | {f"map@{k}": 0.0 for k in self.k_values}
            )

        batch_size = batch_size or self.batch_size
        if batch_size <= 0:
            batch_size = num_students

        positives = _build_edge_map(test_edges, num_students)
        excluded = _build_edge_map(exclude_edges, num_students) if exclude_edges is not None else None

        results_sum = {f"hit_rate@{k}": 0.0 for k in self.k_values}
        results_sum.update({f"ndcg@{k}": 0.0 for k in self.k_values})
        results_sum.update({f"precision@{k}": 0.0 for k in self.k_values})
        results_sum.update({f"recall@{k}": 0.0 for k in self.k_values})
        results_sum.update({f"mrr@{k}": 0.0 for k in self.k_values})
        results_sum.update({f"map@{k}": 0.0 for k in self.k_values})
        total_students = 0

        with torch.no_grad():
            embeddings = model.get_embeddings(data)
            student_emb = embeddings["student"]  # [N, dim]
            problem_emb = embeddings["problem"]  # [M, dim]

            for start in range(0, num_students, batch_size):
                end = min(start + batch_size, num_students)
                batch_student_indices = list(range(start, end))
                batch_student_emb = student_emb[start:end]

                scores = model.predict_link(
                    batch_student_emb.unsqueeze(1)
                    .expand(-1, num_problems, -1)
                    .reshape(-1, batch_student_emb.size(-1)),
                    problem_emb.unsqueeze(0)
                    .expand(end - start, -1, -1)
                    .reshape(-1, problem_emb.size(-1)),
                ).reshape(end - start, num_problems)

                # 排除已知边
                if excluded is not None:
                    for i, student_idx in enumerate(batch_student_indices):
                        blocked = excluded[student_idx]
                        if blocked:
                            scores[i, list(blocked)] = float("-inf")

                # 构建批次 ground_truth
                ground_truth = torch.zeros((end - start, num_problems), device=scores.device)
                active_mask = torch.zeros(end - start, dtype=torch.bool, device=scores.device)
                for i, student_idx in enumerate(batch_student_indices):
                    positive_items = positives[student_idx]
                    if positive_items:
                        ground_truth[i, list(positive_items)] = 1.0
                        active_mask[i] = True
                    elif not ignore_empty_students:
                        active_mask[i] = True  # 计入但为全 0

                # 若全部被忽略则继续下一批
                if not active_mask.any():
                    continue

                scores = scores[active_mask]
                ground_truth = ground_truth[active_mask]
                batch_size_actual = ground_truth.size(0)
                for k in self.k_values:
                    results_sum[f"hit_rate@{k}"] += hit_rate_at_k(scores, ground_truth, k) * batch_size_actual
                    results_sum[f"ndcg@{k}"] += ndcg_at_k(scores, ground_truth, k) * batch_size_actual
                    results_sum[f"precision@{k}"] += precision_at_k(scores, ground_truth, k) * batch_size_actual
                    results_sum[f"recall@{k}"] += recall_at_k(scores, ground_truth, k) * batch_size_actual
                    results_sum[f"mrr@{k}"] += mrr_at_k(scores, ground_truth, k) * batch_size_actual
                    results_sum[f"map@{k}"] += map_at_k(scores, ground_truth, k) * batch_size_actual

                total_students += batch_size_actual

        if total_students == 0:
            return {metric: 0.0 for metric in results_sum}

        return {metric: value / total_students for metric, value in results_sum.items()}

    def evaluate_per_student(
        self,
        model,
        data,
        test_edges: torch.Tensor,
        num_problems: int,
        k: int = 10,
        exclude_edges: Optional[torch.Tensor] = None,
    ) -> Dict[int, Dict[str, float]]:
        """按学生评估

        Args:
            model: 推荐模型
            data: HeteroData
            test_edges: 测试集边 [2, num_edges]
            num_problems: 题目数量
            k: Top-K
            exclude_edges: 需要排除的已知边

        Returns:
            学生索引 -> 评估指标 的字典
        """
        model.eval()

        # 按学生分组测试边
        student_to_problems = {}
        for i in range(test_edges.size(1)):
            student_idx = test_edges[0, i].item()
            problem_idx = test_edges[1, i].item()
            student_to_problems.setdefault(student_idx, []).append(problem_idx)

        excluded = (
            _build_edge_map(exclude_edges, max(student_to_problems.keys()) + 1)
            if exclude_edges is not None and student_to_problems
            else None
        )

        results = {}

        with torch.no_grad():
            embeddings = model.get_embeddings(data)
            problem_emb = embeddings["problem"]  # [M, dim]

            for student_idx, true_problems in student_to_problems.items():
                student_emb = embeddings["student"][student_idx].unsqueeze(0)  # [1, dim]

                # 计算该学生对所有题目的分数
                scores = model.predict_link(
                    student_emb.expand(num_problems, -1),
                    problem_emb,
                )  # [M]

                if excluded is not None and student_idx < len(excluded):
                    blocked = excluded[student_idx]
                    if blocked:
                        scores[list(blocked)] = float("-inf")

                # Top-K 预测
                k_eff = min(k, num_problems)
                if k_eff <= 0:
                    top_k_indices = torch.tensor([], dtype=torch.long, device=scores.device)
                else:
                    _, top_k_indices = torch.topk(scores, k_eff)
                top_k_set = set(top_k_indices.tolist())

                # 计算命中
                hits = len(set(true_problems) & top_k_set)
                hit_rate = hits / len(true_problems) if true_problems else 0
                precision = hits / k if k > 0 else 0

                results[student_idx] = {
                    "hit_rate": hit_rate,
                    "precision": precision,
                    "num_ground_truth": len(true_problems),
                }

        return results
