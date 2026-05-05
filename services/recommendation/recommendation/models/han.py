"""
HAN 推荐模型

基于异构图注意力网络（HAN）的推荐模型。
"""
from typing import Dict, List, Optional, Tuple

import torch
import torch.nn as nn
import torch.nn.functional as F

try:
    from torch_geometric.data import HeteroData
    from torch_geometric.nn import HANConv
    HAS_PYG = True
except ImportError:
    HAS_PYG = False

from .base import BaseGNNModel
from .decoder import BilinearDecoder


class HeteroHAN(nn.Module):
    """异构图 HAN 编码器"""

    def __init__(
        self,
        in_channels_dict: Dict[str, int],
        hidden_dim: int = 64,
        num_layers: int = 1,
        heads: int = 4,
        metadata: Optional[Tuple] = None,
        dropout: float = 0.2,
    ):
        super().__init__()
        if not HAS_PYG:
            raise ImportError("PyTorch Geometric 未安装")
        if hidden_dim % heads != 0:
            raise ValueError("hidden_dim 必须能被 heads 整除")
        if metadata is None:
            raise ValueError("HAN 需要 metadata")

        self.hidden_dim = hidden_dim
        self.num_layers = num_layers
        self.heads = heads
        self.metadata = metadata

        # 输入投影到统一维度
        self.input_projections = nn.ModuleDict()
        for node_type, in_channels in in_channels_dict.items():
            self.input_projections[node_type] = nn.Linear(in_channels, hidden_dim)

        self.convs = nn.ModuleList()
        for _ in range(num_layers):
            self.convs.append(
                HANConv(
                    in_channels=hidden_dim,
                    out_channels=hidden_dim,
                    heads=heads,
                    metadata=metadata,
                    dropout=dropout,
                )
            )

    def forward(self, x_dict: Dict[str, torch.Tensor], edge_index_dict: Dict) -> Dict[str, torch.Tensor]:
        # 先统一投影
        h_dict = {
            node_type: F.relu(self.input_projections[node_type](x))
            for node_type, x in x_dict.items()
        }
        for conv in self.convs:
            h_dict_old = h_dict
            h_dict_new = conv(h_dict, edge_index_dict)
            h_dict = {}
            for node_type, old_emb in h_dict_old.items():
                if node_type in h_dict_new and h_dict_new[node_type] is not None:
                    h_dict[node_type] = F.relu(h_dict_new[node_type])
                else:
                    # 没有收到消息时保留上一层表示
                    h_dict[node_type] = old_emb
        return h_dict


class HANRecommender(BaseGNNModel):
    """基于 HAN 的推荐模型"""

    def __init__(
        self,
        in_channels_dict: Dict[str, int],
        hidden_dim: int = 64,
        num_layers: int = 1,
        heads: int = 4,
        metadata: Optional[Tuple] = None,
        decoder_type: str = 'bilinear',
    ):
        super().__init__(hidden_dim, num_layers)

        if not HAS_PYG:
            raise ImportError("PyTorch Geometric 未安装")

        self.encoder = HeteroHAN(
            in_channels_dict=in_channels_dict,
            hidden_dim=hidden_dim,
            num_layers=num_layers,
            heads=heads,
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
        x_dict = {node_type: data[node_type].x for node_type in data.node_types}
        edge_index_dict = {edge_type: data[edge_type].edge_index for edge_type in data.edge_types}

        embeddings = self.encoder(x_dict, edge_index_dict)
        self._cached_embeddings = embeddings
        return embeddings

    def predict_link(
        self,
        student_emb: torch.Tensor,
        problem_emb: torch.Tensor,
    ) -> torch.Tensor:
        return self.decoder(student_emb, problem_emb)

    def predict_link_logits(
        self,
        student_emb: torch.Tensor,
        problem_emb: torch.Tensor,
    ) -> torch.Tensor:
        if hasattr(self.decoder, "score"):
            return self.decoder.score(student_emb, problem_emb)
        return super().predict_link_logits(student_emb, problem_emb)

    def forward(
        self,
        data: "HeteroData",
        edge_label_index: torch.Tensor,
    ) -> torch.Tensor:
        embeddings = self.get_embeddings(data)
        student_emb = embeddings['student'][edge_label_index[0]]
        problem_emb = embeddings['problem'][edge_label_index[1]]
        return self.predict_link(student_emb, problem_emb)

    @classmethod
    def from_hetero_data(
        cls,
        data: "HeteroData",
        hidden_dim: int = 64,
        num_layers: int = 1,
        heads: int = 4,
        decoder_type: str = 'bilinear',
    ) -> "HANRecommender":
        in_channels_dict = {}
        for node_type in data.node_types:
            if hasattr(data[node_type], 'x') and data[node_type].x is not None:
                in_channels_dict[node_type] = data[node_type].x.size(1)

        metadata = data.metadata()

        return cls(
            in_channels_dict=in_channels_dict,
            hidden_dim=hidden_dim,
            num_layers=num_layers,
            heads=heads,
            metadata=metadata,
            decoder_type=decoder_type,
        )
