"""
链接预测解码器

将节点嵌入转换为链接概率。
"""
import torch
import torch.nn as nn


class BilinearDecoder(nn.Module):
    """双线性解码器

    score = sigmoid(student @ W @ problem.T)

    Attributes:
        hidden_dim: 嵌入维度
    """

    def __init__(self, hidden_dim: int = 64):
        super().__init__()
        self.hidden_dim = hidden_dim
        self.weight = nn.Parameter(torch.randn(hidden_dim, hidden_dim))
        nn.init.xavier_uniform_(self.weight)

    def forward(
        self,
        student_emb: torch.Tensor,
        problem_emb: torch.Tensor
    ) -> torch.Tensor:
        """计算链接概率

        Args:
            student_emb: 学生嵌入 [batch, dim]
            problem_emb: 题目嵌入 [batch, dim]

        Returns:
            链接概率 [batch] (0-1)
        """
        scores = self.score(student_emb, problem_emb)
        return torch.sigmoid(scores)

    def score(
        self,
        student_emb: torch.Tensor,
        problem_emb: torch.Tensor,
    ) -> torch.Tensor:
        """计算未归一化分数（logits）"""
        transformed = student_emb @ self.weight  # [batch, dim]
        return (transformed * problem_emb).sum(dim=-1)  # [batch]


class DotProductDecoder(nn.Module):
    """点积解码器

    score = sigmoid(student · problem)
    """

    def forward(
        self,
        student_emb: torch.Tensor,
        problem_emb: torch.Tensor
    ) -> torch.Tensor:
        """计算链接概率"""
        scores = self.score(student_emb, problem_emb)
        return torch.sigmoid(scores)

    def score(
        self,
        student_emb: torch.Tensor,
        problem_emb: torch.Tensor,
    ) -> torch.Tensor:
        """计算未归一化分数（logits）"""
        return (student_emb * problem_emb).sum(dim=-1)


class MLPDecoder(nn.Module):
    """MLP 解码器

    将拼接的嵌入通过 MLP 预测链接概率。

    Attributes:
        hidden_dim: 嵌入维度
        mlp_hidden: MLP 隐藏层维度
    """

    def __init__(self, hidden_dim: int = 64, mlp_hidden: int = 32):
        super().__init__()
        self.mlp = nn.Sequential(
            nn.Linear(hidden_dim * 2, mlp_hidden),
            nn.ReLU(),
            nn.Dropout(0.2),
        )
        self.out = nn.Linear(mlp_hidden, 1)

    def forward(
        self,
        student_emb: torch.Tensor,
        problem_emb: torch.Tensor
    ) -> torch.Tensor:
        """计算链接概率"""
        scores = self.score(student_emb, problem_emb)
        return torch.sigmoid(scores)

    def score(
        self,
        student_emb: torch.Tensor,
        problem_emb: torch.Tensor,
    ) -> torch.Tensor:
        """计算未归一化分数（logits）"""
        combined = torch.cat([student_emb, problem_emb], dim=-1)  # [batch, dim*2]
        hidden = self.mlp(combined)
        return self.out(hidden).squeeze(-1)  # [batch]
