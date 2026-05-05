"""
R-GCN 推荐模型

基于关系图卷积网络的异构图推荐模型。
"""
from typing import Dict, List, Optional, Tuple

import torch
import torch.nn as nn
import torch.nn.functional as F

try:
    from torch_geometric.data import HeteroData
    from torch_geometric.nn import RGCNConv, HeteroConv, SAGEConv, Linear
    HAS_PYG = True
except ImportError:
    HAS_PYG = False

from .base import BaseGNNModel
from .decoder import BilinearDecoder


class HeteroRGCN(nn.Module):
    """异构图 R-GCN 编码器

    使用 HeteroConv 包装多种关系的卷积层。
    添加反向边支持双向消息传递。

    Attributes:
        hidden_dim: 隐藏层维度
        num_layers: 卷积层数
        metadata: 图的元数据 (node_types, edge_types)
    """

    def __init__(
        self,
        in_channels_dict: Dict[str, int],
        hidden_dim: int = 64,
        num_layers: int = 2,
        metadata: Optional[Tuple] = None,
    ):
        super().__init__()
        self.hidden_dim = hidden_dim
        self.num_layers = num_layers
        self.node_types = list(in_channels_dict.keys())

        # 输入投影层：将不同维度的输入特征映射到统一维度
        self.input_projections = nn.ModuleDict()
        for node_type, in_channels in in_channels_dict.items():
            self.input_projections[node_type] = nn.Linear(in_channels, hidden_dim)

        # 扩展元数据，添加反向边
        self.extended_edge_types = []
        if metadata:
            for edge_type in metadata[1]:
                self.extended_edge_types.append(edge_type)
                # 添加反向边 (dst, rev_rel, src)
                src, rel, dst = edge_type
                rev_edge_type = (dst, f'rev_{rel}', src)
                if rev_edge_type not in metadata[1]:
                    self.extended_edge_types.append(rev_edge_type)

        # 卷积层
        self.convs = nn.ModuleList()
        for i in range(num_layers):
            conv_dict = {}
            for edge_type in self.extended_edge_types:
                # 使用 SAGEConv 作为基础卷积
                conv_dict[edge_type] = SAGEConv(hidden_dim, hidden_dim)
            self.convs.append(HeteroConv(conv_dict, aggr='mean'))

    def forward(self, x_dict: Dict[str, torch.Tensor], edge_index_dict: Dict) -> Dict[str, torch.Tensor]:
        """前向传播

        Args:
            x_dict: 节点类型 -> 特征张量
            edge_index_dict: 边类型 -> 边索引

        Returns:
            节点类型 -> 嵌入张量
        """
        # 输入投影
        h_dict = {}
        for node_type, x in x_dict.items():
            if node_type in self.input_projections:
                h_dict[node_type] = F.relu(self.input_projections[node_type](x))
            else:
                h_dict[node_type] = x

        # 扩展边索引，添加反向边
        extended_edge_index_dict = dict(edge_index_dict)
        for edge_type, edge_index in edge_index_dict.items():
            src, rel, dst = edge_type
            rev_edge_type = (dst, f'rev_{rel}', src)
            if rev_edge_type not in extended_edge_index_dict:
                # 反转边方向
                extended_edge_index_dict[rev_edge_type] = edge_index.flip(0)

        # 多层卷积
        for conv in self.convs:
            # 保存原始嵌入用于残差连接
            h_dict_old = h_dict.copy()

            h_dict_new = conv(h_dict, extended_edge_index_dict)

            # 合并：如果节点类型没有更新，保留原始嵌入
            for node_type in self.node_types:
                if node_type in h_dict_new and h_dict_new[node_type] is not None:
                    h_dict[node_type] = F.relu(h_dict_new[node_type])
                # 如果没有更新，保持原来的 h_dict[node_type]

        return h_dict


class RGCNRecommender(BaseGNNModel):
    """基于 R-GCN 的推荐模型

    完整的推荐模型，包含编码器和解码器。

    Attributes:
        encoder: HeteroRGCN 编码器
        decoder: 链接预测解码器
    """

    def __init__(
        self,
        in_channels_dict: Dict[str, int],
        hidden_dim: int = 64,
        num_layers: int = 2,
        metadata: Optional[Tuple] = None,
        decoder_type: str = 'bilinear',
    ):
        """初始化模型

        Args:
            in_channels_dict: 节点类型 -> 输入特征维度
            hidden_dim: 隐藏层维度
            num_layers: 卷积层数
            metadata: 图元数据 (node_types, edge_types)
            decoder_type: 解码器类型 ('bilinear', 'dot', 'mlp')
        """
        super().__init__(hidden_dim, num_layers)

        if not HAS_PYG:
            raise ImportError("PyTorch Geometric 未安装")

        self.encoder = HeteroRGCN(
            in_channels_dict=in_channels_dict,
            hidden_dim=hidden_dim,
            num_layers=num_layers,
            metadata=metadata,
        )

        if decoder_type == 'bilinear':
            self.decoder = BilinearDecoder(hidden_dim)
        elif decoder_type == 'dot':
            from .decoder import DotProductDecoder
            self.decoder = DotProductDecoder()
        elif decoder_type == 'mlp':
            from .decoder import MLPDecoder
            self.decoder = MLPDecoder(hidden_dim)
        else:
            raise ValueError(f"未知的解码器类型: {decoder_type}")

        self._cached_embeddings = None

    def get_embeddings(self, data: "HeteroData") -> Dict[str, torch.Tensor]:
        """获取节点嵌入"""
        x_dict = {node_type: data[node_type].x for node_type in data.node_types}
        edge_index_dict = {edge_type: data[edge_type].edge_index for edge_type in data.edge_types}

        embeddings = self.encoder(x_dict, edge_index_dict)
        self._cached_embeddings = embeddings
        return embeddings

    def predict_link(
        self,
        student_emb: torch.Tensor,
        problem_emb: torch.Tensor
    ) -> torch.Tensor:
        """预测链接概率"""
        return self.decoder(student_emb, problem_emb)

    def predict_link_logits(
        self,
        student_emb: torch.Tensor,
        problem_emb: torch.Tensor,
    ) -> torch.Tensor:
        """预测链接未归一化分数（logits）"""
        if hasattr(self.decoder, "score"):
            return self.decoder.score(student_emb, problem_emb)
        return super().predict_link_logits(student_emb, problem_emb)

    def forward(
        self,
        data: "HeteroData",
        edge_label_index: torch.Tensor,
    ) -> torch.Tensor:
        """前向传播用于训练

        Args:
            data: HeteroData 对象
            edge_label_index: 要预测的边 [2, num_edges]
                第一行是学生索引，第二行是题目索引

        Returns:
            链接概率 [num_edges]
        """
        embeddings = self.get_embeddings(data)

        student_emb = embeddings['student'][edge_label_index[0]]
        problem_emb = embeddings['problem'][edge_label_index[1]]

        return self.predict_link(student_emb, problem_emb)

    @classmethod
    def from_hetero_data(
        cls,
        data: "HeteroData",
        hidden_dim: int = 64,
        num_layers: int = 2,
        decoder_type: str = 'bilinear',
    ) -> "RGCNRecommender":
        """从 HeteroData 创建模型

        自动推断输入维度和元数据。

        Args:
            data: HeteroData 对象
            hidden_dim: 隐藏层维度
            num_layers: 卷积层数
            decoder_type: 解码器类型

        Returns:
            初始化的模型
        """
        in_channels_dict = {}
        for node_type in data.node_types:
            if hasattr(data[node_type], 'x') and data[node_type].x is not None:
                in_channels_dict[node_type] = data[node_type].x.size(1)

        metadata = data.metadata()

        return cls(
            in_channels_dict=in_channels_dict,
            hidden_dim=hidden_dim,
            num_layers=num_layers,
            metadata=metadata,
            decoder_type=decoder_type,
        )
