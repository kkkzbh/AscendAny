"""
训练器

管理模型训练流程。
"""
from pathlib import Path
from typing import Dict, Optional
import logging

import torch
import torch.nn as nn
import torch.optim as optim

try:
    from torch_geometric.data import HeteroData
    HAS_PYG = True
except ImportError:
    HAS_PYG = False

from .evaluator import Evaluator

logger = logging.getLogger(__name__)


class Trainer:
    """模型训练器

    管理训练循环、损失计算、早停和检查点保存。

    Attributes:
        model: 推荐模型
        optimizer: 优化器
        criterion: 损失函数
        device: 计算设备
    """

    def __init__(
        self,
        model: nn.Module,
        learning_rate: float = 0.01,
        weight_decay: float = 1e-5,
        loss_type: str = "bce",
        temperature: float = 1.0,
        aux_score_rate_weight: float = 0.0,
        graph_reg_weight: float = 0.0,
        contrastive_weight: float = 0.0,
        contrastive_temperature: float = 0.2,
        contrastive_dropout: float = 0.1,
        contrastive_sample_size: int = 512,
        device: str = 'cpu',
    ):
        """初始化训练器

        Args:
            model: 推荐模型
            learning_rate: 学习率
            weight_decay: L2 正则化系数
            device: 计算设备 ('cpu' 或 'cuda')
        """
        self.device = torch.device(device)
        self.model = model.to(self.device)
        self.optimizer = optim.Adam(
            model.parameters(),
            lr=learning_rate,
            weight_decay=weight_decay
        )
        if loss_type not in {"bce", "listwise"}:
            raise ValueError(f"未知的损失类型: {loss_type}")
        if temperature <= 0:
            raise ValueError("temperature 必须大于 0")
        if aux_score_rate_weight < 0:
            raise ValueError("aux_score_rate_weight 必须 >= 0")
        if graph_reg_weight < 0:
            raise ValueError("graph_reg_weight 必须 >= 0")
        if contrastive_weight < 0:
            raise ValueError("contrastive_weight 必须 >= 0")
        if contrastive_temperature <= 0:
            raise ValueError("contrastive_temperature 必须 > 0")
        if not (0 <= contrastive_dropout < 1):
            raise ValueError("contrastive_dropout 必须在 [0, 1) 内")
        if contrastive_sample_size <= 0:
            raise ValueError("contrastive_sample_size 必须 > 0")
        self.loss_type = loss_type
        self.temperature = temperature
        self.aux_score_rate_weight = aux_score_rate_weight
        self.graph_reg_weight = graph_reg_weight
        self.contrastive_weight = contrastive_weight
        self.contrastive_temperature = contrastive_temperature
        self.contrastive_dropout = contrastive_dropout
        self.contrastive_sample_size = contrastive_sample_size
        self.criterion = nn.BCELoss(reduction="none") if loss_type == "bce" else None

        self.evaluator = Evaluator()
        self.best_val_score = 0.0
        self.patience_counter = 0

    def train_epoch(
        self,
        data: "HeteroData",
        train_edges: torch.Tensor,
        negative_edges: torch.Tensor,
        positive_weights: Optional[torch.Tensor] = None,
        positive_targets: Optional[torch.Tensor] = None,
        batch_size: int = 512,
        num_negatives: int = 1,
    ) -> float:
        """训练一个 epoch

        Args:
            data: HeteroData
            train_edges: 训练集正边 [2, num_pos]
            negative_edges: 负采样边 [2, num_neg]
            batch_size: 批次大小

        Returns:
            平均损失
        """
        if self.loss_type == "listwise":
            return self._train_epoch_listwise(
                data,
                train_edges,
                negative_edges,
                positive_weights=positive_weights,
                positive_targets=positive_targets,
                batch_size=batch_size,
                num_negatives=num_negatives,
            )
        return self._train_epoch_bce(
            data,
            train_edges,
            negative_edges,
            positive_weights=positive_weights,
            positive_targets=positive_targets,
            batch_size=batch_size,
        )

    def _train_epoch_bce(
        self,
        data: "HeteroData",
        train_edges: torch.Tensor,
        negative_edges: torch.Tensor,
        positive_weights: Optional[torch.Tensor] = None,
        positive_targets: Optional[torch.Tensor] = None,
        batch_size: int = 512,
    ) -> float:
        self.model.train()
        data = data.to(self.device)

        # 合并正负样本
        all_edges = torch.cat([train_edges, negative_edges], dim=1).to(self.device)
        labels = torch.cat([
            torch.ones(train_edges.size(1)),
            torch.zeros(negative_edges.size(1)),
        ]).to(self.device)

        weights = None
        aux_targets = None
        if positive_weights is not None:
            pos_weights = positive_weights.to(self.device)
            if pos_weights.numel() != train_edges.size(1):
                raise ValueError("positive_weights 长度必须等于正样本数量")
            weights = torch.cat([
                pos_weights,
                torch.ones(negative_edges.size(1), device=self.device),
            ])
        if positive_targets is not None and self.aux_score_rate_weight > 0:
            pos_targets = positive_targets.to(self.device)
            if pos_targets.numel() != train_edges.size(1):
                raise ValueError("positive_targets 长度必须等于正样本数量")
            aux_targets = torch.cat([
                pos_targets,
                torch.zeros(negative_edges.size(1), device=self.device),
            ])

        # 打乱顺序
        perm = torch.randperm(all_edges.size(1))
        all_edges = all_edges[:, perm]
        labels = labels[perm]
        if weights is not None:
            weights = weights[perm]
        if aux_targets is not None:
            aux_targets = aux_targets[perm]

        total_loss = 0.0
        num_batches = 0

        for i in range(0, all_edges.size(1), batch_size):
            batch_edges = all_edges[:, i:i+batch_size]
            batch_labels = labels[i:i+batch_size]

            self.optimizer.zero_grad()

            # 前向传播
            embeddings = self.model.get_embeddings(data)
            student_emb = embeddings["student"][batch_edges[0]]
            problem_emb = embeddings["problem"][batch_edges[1]]
            predictions = self.model.predict_link(student_emb, problem_emb)

            # 损失计算
            loss = self.criterion(predictions, batch_labels)
            if weights is not None:
                batch_weights = weights[i:i+batch_edges.size(1)]
                loss = (loss * batch_weights).mean()
            else:
                loss = loss.mean()

            if aux_targets is not None and self.aux_score_rate_weight > 0:
                batch_targets = aux_targets[i:i+batch_edges.size(1)]
                pos_mask = batch_labels > 0.5
                if pos_mask.any():
                    aux_loss = nn.functional.mse_loss(
                        predictions[pos_mask],
                        batch_targets[pos_mask],
                    )
                    loss = loss + self.aux_score_rate_weight * aux_loss

            graph_loss = self._graph_reg_loss(embeddings, data)
            if graph_loss is not None:
                loss = loss + self.graph_reg_weight * graph_loss

            contrastive_loss = self._contrastive_loss(embeddings)
            if contrastive_loss is not None:
                loss = loss + self.contrastive_weight * contrastive_loss

            # 反向传播
            loss.backward()
            self.optimizer.step()

            total_loss += loss.item()
            num_batches += 1

        return total_loss / num_batches if num_batches > 0 else 0.0

    def _train_epoch_listwise(
        self,
        data: "HeteroData",
        train_edges: torch.Tensor,
        negative_edges: torch.Tensor,
        positive_weights: Optional[torch.Tensor] = None,
        positive_targets: Optional[torch.Tensor] = None,
        batch_size: int = 512,
        num_negatives: int = 1,
    ) -> float:
        if num_negatives <= 0:
            raise ValueError("num_negatives 必须大于 0")

        self.model.train()
        data = data.to(self.device)

        num_pos = train_edges.size(1)
        num_neg = negative_edges.size(1)
        expected_neg = num_pos * num_negatives
        if num_neg != expected_neg:
            raise ValueError(
                f"负样本数量不匹配: 期望 {expected_neg}, 实际 {num_neg}"
            )

        train_edges = train_edges.to(self.device)
        negative_edges = negative_edges.to(self.device)
        neg_grouped = negative_edges.reshape(2, num_pos, num_negatives)

        perm = torch.randperm(num_pos, device=self.device)
        train_edges = train_edges[:, perm]
        neg_grouped = neg_grouped[:, perm, :]

        pos_weights = None
        if positive_weights is not None:
            pos_weights = positive_weights.to(self.device)
            if pos_weights.numel() != num_pos:
                raise ValueError("positive_weights 长度必须等于正样本数量")
            pos_weights = pos_weights[perm]
        pos_targets = None
        if positive_targets is not None and self.aux_score_rate_weight > 0:
            pos_targets = positive_targets.to(self.device)
            if pos_targets.numel() != num_pos:
                raise ValueError("positive_targets 长度必须等于正样本数量")
            pos_targets = pos_targets[perm]

        total_loss = 0.0
        num_batches = 0

        for start in range(0, num_pos, batch_size):
            end = min(start + batch_size, num_pos)
            batch_pos = train_edges[:, start:end]  # [2, B]
            batch_neg = neg_grouped[:, start:end, :]  # [2, B, K]

            edges_list = []
            batch_size_actual = end - start
            for i in range(batch_size_actual):
                edges_list.append(batch_pos[:, i:i+1])
                edges_list.append(batch_neg[:, i, :])
            batch_edges = torch.cat(edges_list, dim=1)  # [2, B*(1+K)]

            self.optimizer.zero_grad()

            embeddings = self.model.get_embeddings(data)
            student_emb = embeddings["student"][batch_edges[0]]
            problem_emb = embeddings["problem"][batch_edges[1]]
            logits = self.model.predict_link_logits(student_emb, problem_emb)
            logits = logits.view(batch_size_actual, 1 + num_negatives)
            logits = logits / self.temperature

            log_probs = torch.log_softmax(logits, dim=1)
            loss = -log_probs[:, 0]
            if pos_weights is not None:
                loss = loss * pos_weights[start:end]
            loss = loss.mean()

            if pos_targets is not None and self.aux_score_rate_weight > 0:
                pos_logits = logits[:, 0]
                pos_probs = torch.sigmoid(pos_logits)
                aux_loss = nn.functional.mse_loss(pos_probs, pos_targets[start:end])
                loss = loss + self.aux_score_rate_weight * aux_loss

            graph_loss = self._graph_reg_loss(embeddings, data)
            if graph_loss is not None:
                loss = loss + self.graph_reg_weight * graph_loss

            contrastive_loss = self._contrastive_loss(embeddings)
            if contrastive_loss is not None:
                loss = loss + self.contrastive_weight * contrastive_loss

            loss.backward()
            self.optimizer.step()

            total_loss += loss.item()
            num_batches += 1

        return total_loss / num_batches if num_batches > 0 else 0.0

    def _graph_reg_loss(self, embeddings: Dict[str, torch.Tensor], data: "HeteroData") -> Optional[torch.Tensor]:
        if self.graph_reg_weight <= 0:
            return None

        edge_types = [
            ("knowledge", "parent", "knowledge"),
            ("knowledge", "prerequisite", "knowledge"),
        ]
        losses = []
        for edge_type in edge_types:
            if edge_type not in data.edge_types:
                continue
            edge_index = data[edge_type].edge_index.to(self.device)
            if edge_index.numel() == 0:
                continue
            src, dst = edge_index
            emb = embeddings[edge_type[0]]
            diff = emb[src] - emb[dst]
            losses.append(diff.pow(2).sum(dim=1).mean())

        if not losses:
            return None
        return sum(losses) / len(losses)

    def _contrastive_loss(self, embeddings: Dict[str, torch.Tensor]) -> Optional[torch.Tensor]:
        if self.contrastive_weight <= 0:
            return None

        losses = []
        for node_type in ("student", "problem", "knowledge"):
            if node_type not in embeddings:
                continue
            z = embeddings[node_type]
            if z.numel() == 0:
                continue
            if z.size(0) > self.contrastive_sample_size:
                idx = torch.randperm(z.size(0), device=z.device)[: self.contrastive_sample_size]
                z = z[idx]
            z1 = nn.functional.normalize(z, dim=1)
            z2 = nn.functional.dropout(z1, p=self.contrastive_dropout, training=True)
            z2 = nn.functional.normalize(z2, dim=1)
            logits = (z1 @ z2.t()) / self.contrastive_temperature
            labels = torch.arange(z1.size(0), device=z.device)
            losses.append(nn.functional.cross_entropy(logits, labels))

        if not losses:
            return None
        return sum(losses) / len(losses)

    def evaluate(
        self,
        data: "HeteroData",
        val_edges: torch.Tensor,
        num_students: int,
        num_problems: int,
        exclude_edges: Optional[torch.Tensor] = None,
        ignore_empty_students: bool = False,
    ) -> Dict[str, float]:
        """评估模型

        Args:
            data: HeteroData
            val_edges: 验证集边 [2, num_edges]
            num_students: 学生数量
            num_problems: 题目数量
            exclude_edges: 需要排除的已知边（如训练集）

        Returns:
            评估指标字典
        """
        data = data.to(self.device)
        val_edges = val_edges.to(self.device)

        return self.evaluator.evaluate(
            self.model,
            data,
            val_edges,
            num_students,
            num_problems,
            exclude_edges=exclude_edges,
            ignore_empty_students=ignore_empty_students,
        )

    def train(
        self,
        data: "HeteroData",
        train_edges: torch.Tensor,
        val_edges: torch.Tensor,
        negative_sampler,
        num_students: int,
        num_problems: int,
        positive_weights: Optional[torch.Tensor] = None,
        positive_targets: Optional[torch.Tensor] = None,
        epochs: int = 100,
        batch_size: int = 512,
        patience: int = 10,
        num_negatives: int = 1,
        save_path: Optional[Path] = None,
        eval_ignore_empty: bool = False,
    ) -> Dict[str, list]:
        """完整训练流程

        Args:
            data: HeteroData
            train_edges: 训练集边
            val_edges: 验证集边
            negative_sampler: 负采样函数
            num_students: 学生数量
            num_problems: 题目数量
            positive_weights: 正样本权重（可选）
            positive_targets: 正样本回归目标（可选）
            epochs: 训练轮数
            batch_size: 批次大小
            patience: 早停耐心值
            save_path: 模型保存路径
            eval_ignore_empty: 评估时是否忽略测试/验证集中无正样本的学生

        Returns:
            训练历史记录
        """
        history = {
            'train_loss': [],
            'val_hit_rate@10': [],
            'val_ndcg@10': [],
            'val_precision@10': [],
            'val_recall@10': [],
            'val_mrr@10': [],
            'val_map@10': [],
        }

        for epoch in range(epochs):
            # 每个 epoch 重新采样负样本
            negative_edges = negative_sampler()

            # 训练
            train_loss = self.train_epoch(
                data,
                train_edges,
                negative_edges,
                positive_weights=positive_weights,
                positive_targets=positive_targets,
                batch_size=batch_size,
                num_negatives=num_negatives,
            )
            history['train_loss'].append(train_loss)

            # 评估
            val_metrics = self.evaluate(
                data,
                val_edges,
                num_students,
                num_problems,
                exclude_edges=train_edges,
                ignore_empty_students=eval_ignore_empty,
            )
            history['val_hit_rate@10'].append(val_metrics.get('hit_rate@10', 0))
            history['val_ndcg@10'].append(val_metrics.get('ndcg@10', 0))
            history['val_precision@10'].append(val_metrics.get('precision@10', 0))
            history['val_recall@10'].append(val_metrics.get('recall@10', 0))
            history['val_mrr@10'].append(val_metrics.get('mrr@10', 0))
            history['val_map@10'].append(val_metrics.get('map@10', 0))

            logger.info(
                f"Epoch {epoch+1}/{epochs} | "
                f"Loss: {train_loss:.4f} | "
                f"Val Hit@10: {val_metrics.get('hit_rate@10', 0):.4f} | "
                f"Val NDCG@10: {val_metrics.get('ndcg@10', 0):.4f} | "
                f"Val Precision@10: {val_metrics.get('precision@10', 0):.4f} | "
                f"Val Recall@10: {val_metrics.get('recall@10', 0):.4f} | "
                f"Val MRR@10: {val_metrics.get('mrr@10', 0):.4f} | "
                f"Val MAP@10: {val_metrics.get('map@10', 0):.4f}"
            )

            # 早停检查
            current_score = val_metrics.get('hit_rate@10', 0)
            if current_score > self.best_val_score:
                self.best_val_score = current_score
                self.patience_counter = 0

                # 保存最佳模型
                if save_path:
                    self.save_checkpoint(save_path)
                    logger.info(f"保存最佳模型到 {save_path}")
            else:
                self.patience_counter += 1
                if self.patience_counter >= patience:
                    logger.info(f"早停：验证集性能 {patience} 轮未提升")
                    break

        return history

    def save_checkpoint(self, path: Path):
        """保存模型检查点"""
        path = Path(path)
        path.parent.mkdir(parents=True, exist_ok=True)

        torch.save({
            'model_state_dict': self.model.state_dict(),
            'optimizer_state_dict': self.optimizer.state_dict(),
            'best_val_score': self.best_val_score,
        }, path)

    def load_checkpoint(self, path: Path):
        """加载模型检查点"""
        checkpoint = torch.load(path, map_location=self.device)
        self.model.load_state_dict(checkpoint['model_state_dict'])
        self.optimizer.load_state_dict(checkpoint['optimizer_state_dict'])
        self.best_val_score = checkpoint.get('best_val_score', 0.0)
