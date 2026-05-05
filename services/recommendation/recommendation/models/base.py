"""
模型基类

定义异构图推荐模型的统一接口。
"""
from abc import ABC, abstractmethod
from typing import Dict, List, Optional, Tuple

import torch
import torch.nn as nn

try:
    from torch_geometric.data import HeteroData
    HAS_PYG = True
except ImportError:
    HAS_PYG = False


class BaseGNNModel(nn.Module, ABC):
    """异构图推荐模型基类

    定义统一接口，支持策略模式切换不同模型实现。

    Attributes:
        hidden_dim: 隐藏层维度
        num_layers: GNN 层数
    """

    def __init__(self, hidden_dim: int = 64, num_layers: int = 2):
        super().__init__()
        self.hidden_dim = hidden_dim
        self.num_layers = num_layers

    @abstractmethod
    def get_embeddings(self, data: "HeteroData") -> Dict[str, torch.Tensor]:
        """获取各类型节点的嵌入向量

        Args:
            data: PyG HeteroData 对象

        Returns:
            节点类型 -> 嵌入张量 的字典
            例: {'student': [N, hidden_dim], 'problem': [M, hidden_dim]}
        """
        pass

    @abstractmethod
    def predict_link(
        self,
        student_emb: torch.Tensor,
        problem_emb: torch.Tensor
    ) -> torch.Tensor:
        """预测学生-题目链接概率

        Args:
            student_emb: 学生嵌入 [batch, hidden_dim]
            problem_emb: 题目嵌入 [batch, hidden_dim]

        Returns:
            链接概率 [batch] (0-1)
        """
        pass

    def predict_link_logits(
        self,
        student_emb: torch.Tensor,
        problem_emb: torch.Tensor,
    ) -> torch.Tensor:
        """预测学生-题目链接的未归一化分数（logits）"""
        scores = self.predict_link(student_emb, problem_emb)
        return torch.logit(scores.clamp(1e-6, 1 - 1e-6))

    def recommend(
        self,
        data: "HeteroData",
        student_idx: int,
        exclude_problems: Optional[List[int]] = None,
        top_k: int = 10
    ) -> List[Tuple[int, float]]:
        """为指定学生推荐题目

        Args:
            data: PyG HeteroData 对象
            student_idx: 学生索引
            exclude_problems: 要排除的题目索引列表（如已做过的题）
            top_k: 返回的推荐数量

        Returns:
            (题目索引, 置信度) 列表，按置信度降序
        """
        self.eval()
        device = next(self.parameters()).device
        data = data.to(device)
        with torch.no_grad():
            embeddings = self.get_embeddings(data)
            student_emb = embeddings['student'][student_idx].unsqueeze(0)  # [1, dim]
            problem_emb = embeddings['problem']  # [M, dim]

            # 计算所有题目的链接概率
            scores = self.predict_link(
                student_emb.expand(problem_emb.size(0), -1),
                problem_emb
            )  # [M]

            # 排除已做题目
            if exclude_problems:
                mask = torch.ones_like(scores, dtype=torch.bool)
                mask[exclude_problems] = False
                scores = scores * mask.float() - (~mask).float() * 1e9

            # Top-K
            top_scores, top_indices = torch.topk(scores, min(top_k, scores.size(0)))

            return [(idx.item(), score.item()) for idx, score in zip(top_indices, top_scores)]
