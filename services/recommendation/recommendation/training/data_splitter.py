"""
数据划分工具

将边数据划分为训练/验证/测试集，并生成负样本。
"""
from typing import Dict, List, Optional, Tuple
import random

import torch
import pandas as pd
import numpy as np

try:
    from torch_geometric.data import HeteroData
    HAS_PYG = True
except ImportError:
    HAS_PYG = False


class DataSplitter:
    """数据划分器

    支持按时间或随机划分提交边。

    Attributes:
        submissions: 提交记录 DataFrame
        student_to_idx: 学生昵称 -> 索引
        problem_to_idx: 题目ID -> 索引
    """

    def __init__(
        self,
        submissions: pd.DataFrame,
        student_to_idx: Dict[str, int],
        problem_to_idx: Dict[str, int],
        time_column: str = 'time',
        student_column: str = 'nickname',
        problem_column: str = 'global_problem_id',
    ):
        self.submissions = submissions
        self.student_to_idx = student_to_idx
        self.problem_to_idx = problem_to_idx
        self.time_column = time_column
        self.student_column = student_column
        self.problem_column = problem_column

        # 构建边列表
        self._build_edges()

    def _build_edges(self):
        """从提交记录构建边列表"""
        edges = []
        for _, row in self.submissions.iterrows():
            student = row[self.student_column]
            problem = str(row[self.problem_column])

            if student in self.student_to_idx and problem in self.problem_to_idx:
                score_rate = row.get('score_rate')
                if pd.isna(score_rate):
                    score_rate = 1.0 if row.get('is_correct', False) else 0.0
                edges.append({
                    'student_idx': self.student_to_idx[student],
                    'problem_idx': self.problem_to_idx[problem],
                    'time': row.get(self.time_column),
                    'is_correct': row.get('is_correct', True),
                    'score': row.get('score', 0),
                    'score_rate': score_rate,
                })

        self.edges_df = pd.DataFrame(edges)

        # 去重：每个 (student, problem) 对只保留一条（最后一次提交）
        if self.time_column and self.time_column in self.submissions.columns:
            self.edges_df = self.edges_df.sort_values('time')
        self.unique_edges = self.edges_df.drop_duplicates(
            subset=['student_idx', 'problem_idx'],
            keep='last'
        ).reset_index(drop=True)

    def split_by_time(
        self,
        train_end: str,
        val_end: Optional[str] = None,
    ) -> Tuple[pd.DataFrame, pd.DataFrame, pd.DataFrame]:
        """按时间划分

        Args:
            train_end: 训练集截止时间（如 '2025-01-01'）
            val_end: 验证集截止时间，None 则随机划分剩余部分

        Returns:
            (训练集, 验证集, 测试集) 边 DataFrame
        """
        df = self.unique_edges.copy()
        df['time'] = pd.to_datetime(df['time'])

        train_mask = df['time'] < train_end

        if val_end:
            val_mask = (df['time'] >= train_end) & (df['time'] < val_end)
            test_mask = df['time'] >= val_end
        else:
            # 剩余部分随机划分
            remaining = df[~train_mask]
            indices = remaining.index.tolist()
            random.shuffle(indices)
            val_size = len(indices) // 2
            val_indices = set(indices[:val_size])
            val_mask = df.index.isin(val_indices)
            test_mask = (~train_mask) & (~val_mask)

        return df[train_mask], df[val_mask], df[test_mask]

    def split_random(
        self,
        train_ratio: float = 0.8,
        val_ratio: float = 0.1,
        seed: int = 42,
    ) -> Tuple[pd.DataFrame, pd.DataFrame, pd.DataFrame]:
        """随机划分

        Args:
            train_ratio: 训练集比例
            val_ratio: 验证集比例
            seed: 随机种子

        Returns:
            (训练集, 验证集, 测试集) 边 DataFrame
        """
        df = self.unique_edges.copy()
        n = len(df)

        random.seed(seed)
        indices = list(range(n))
        random.shuffle(indices)

        train_end = int(n * train_ratio)
        val_end = int(n * (train_ratio + val_ratio))

        train_indices = set(indices[:train_end])
        val_indices = set(indices[train_end:val_end])

        train_mask = df.index.isin(train_indices)
        val_mask = df.index.isin(val_indices)
        test_mask = ~(train_mask | val_mask)

        return df[train_mask], df[val_mask], df[test_mask]

    def split_leave_k_out(
        self,
        k: int = 1,
        val_ratio: float = 0.1,
        seed: int = 42,
    ) -> Tuple[pd.DataFrame, pd.DataFrame, pd.DataFrame]:
        """按学生留出 K 条交互作为测试，剩余再切分验证

        Args:
            k: 每个学生在测试集中至少保留的正样本数量（若不足则全留训练）
            val_ratio: 训练剩余部分划入验证的比例
            seed: 随机种子

        Returns:
            (训练集, 验证集, 测试集)
        """
        if k <= 0:
            raise ValueError("k 必须大于 0")

        rng = random.Random(seed)
        train_rows = []
        val_rows = []
        test_rows = []

        for _, group in self.unique_edges.groupby("student_idx"):
            indices = list(group.index)
            rng.shuffle(indices)

            # 若交互不足，全部放入训练，避免空用户
            if len(indices) <= k:
                test_idx = []
                remaining = indices
            else:
                test_idx = indices[:k]
                remaining = indices[k:]

            # 从剩余中抽取验证
            val_count = max(0, int(len(remaining) * val_ratio))
            val_idx = remaining[:val_count]
            train_idx = remaining[val_count:]

            test_rows.append(self.unique_edges.loc[test_idx])
            val_rows.append(self.unique_edges.loc[val_idx])
            train_rows.append(self.unique_edges.loc[train_idx])

        train_df = pd.concat(train_rows, ignore_index=True) if train_rows else pd.DataFrame()
        val_df = pd.concat(val_rows, ignore_index=True) if val_rows else pd.DataFrame()
        test_df = pd.concat(test_rows, ignore_index=True) if test_rows else pd.DataFrame()

        return train_df, val_df, test_df

    def to_edge_index(self, edges_df: pd.DataFrame) -> torch.Tensor:
        """将边 DataFrame 转换为 edge_index 张量"""
        if edges_df.empty:
            return torch.empty((2, 0), dtype=torch.long)

        edge_array = np.stack(
            [
                edges_df["student_idx"].to_numpy(dtype=np.int64),
                edges_df["problem_idx"].to_numpy(dtype=np.int64),
            ],
            axis=0,
        )
        return torch.from_numpy(edge_array).long()

    def generate_negative_samples(
        self,
        positive_edges: pd.DataFrame,
        num_negatives: int = 1,
        seed: Optional[int] = None,
    ) -> pd.DataFrame:
        """生成负样本

        为每条正边生成指定数量的负边（学生未做过的题目）。

        Args:
            positive_edges: 正边 DataFrame
            num_negatives: 每条正边对应的负边数量
            seed: 随机种子

        Returns:
            负边 DataFrame
        """
        if seed is not None:
            random.seed(seed)

        # 已存在的边集合
        existing_edges = set(
            (row['student_idx'], row['problem_idx'])
            for _, row in self.unique_edges.iterrows()
        )

        num_problems = len(self.problem_to_idx)
        negative_samples = []
        columns = ["student_idx", "problem_idx", "is_correct", "score"]
        if num_problems <= 0 or positive_edges.empty:
            return pd.DataFrame(columns=columns)

        for _, row in positive_edges.iterrows():
            student_idx = row['student_idx']
            positive_problem_idx = row["problem_idx"]
            count = 0
            attempts = 0

            while count < num_negatives and attempts < num_negatives * 10:
                problem_idx = random.randint(0, num_problems - 1)
                if (student_idx, problem_idx) not in existing_edges:
                    negative_samples.append({
                        'student_idx': student_idx,
                        'problem_idx': problem_idx,
                        'is_correct': False,
                        'score': 0,
                    })
                    count += 1
                attempts += 1

            # In tiny or fully observed local graphs there may be no truly
            # unobserved problem for a student. Keep the training job runnable
            # by drawing a weak negative from other problems, while avoiding the
            # current positive item.
            fallback_candidates = [
                problem_idx
                for problem_idx in range(num_problems)
                if problem_idx != positive_problem_idx
            ]
            while count < num_negatives and fallback_candidates:
                negative_samples.append({
                    'student_idx': student_idx,
                    'problem_idx': random.choice(fallback_candidates),
                    'is_correct': False,
                    'score': 0,
                })
                count += 1

        return pd.DataFrame(negative_samples, columns=columns)
