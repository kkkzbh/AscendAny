const iconModules = import.meta.glob<string>(
  "../../assets/path-icons/part-*/icons/*.png",
  {
    eager: true,
    import: "default",
    query: "?url",
  },
);

const POINT_ICON_SLUGS: Record<string, string> = {
  基本概念: "basic-concepts",
  时间复杂度: "time-complexity",
  空间复杂度: "space-complexity",
  递归: "recursion",
  线性结构: "linear-structure",
  数组: "array",
  多维数组: "multidimensional-array",
  矩阵: "matrix",
  链表: "linked-list",
  栈: "stack",
  单调栈: "monotonic-stack",
  队列: "queue",
  单调队列: "monotonic-queue",
  字符串: "string",
  KMP算法: "kmp",
  树: "tree",
  二叉树: "binary-tree",
  树的遍历: "tree-traversal",
  先序遍历: "preorder",
  中序遍历: "inorder",
  后序遍历: "postorder",
  层次遍历: "level-order",
  森林: "forest",
  哈夫曼树: "huffman-tree",
  堆: "heap",
  模拟: "simulation",
  基础模拟: "basic-simulation",
  高级模拟: "advanced-simulation",
  搜索: "search",
  DFS: "dfs",
  BFS: "bfs",
  双向BFS: "bidirectional-bfs",
  图: "graph",
  邻接矩阵: "adjacency-matrix",
  邻接表: "adjacency-list",
  最短路: "shortest-path",
  Dijkstra: "dijkstra",
  SPFA: "spfa",
  Floyd: "floyd",
  拓扑排序: "topological-sort",
  最小生成树: "minimum-spanning-tree",
  Kruskal: "kruskal",
  Prim: "prim",
  数据结构: "data-structure",
  线段树: "segment-tree",
  对顶堆: "dual-heap",
  STL: "stl",
  平衡树: "balanced-tree",
  并查集: "union-find",
  算法: "algorithm",
  贪心: "greedy",
  动态规划: "dynamic-programming",
  二分: "binary-search",
  双指针: "two-pointers",
  前缀和: "prefix-sum",
  差分: "difference-array",
};

const ICONS_BY_SLUG = Object.fromEntries(
  Object.entries(iconModules).map(([path, url]) => {
    const filename = path.split("/").pop() ?? "";
    return [filename.replace(/\.png$/i, ""), url];
  }),
);

export function getPathIconSrc(point: string): string | null {
  const slug = POINT_ICON_SLUGS[point.trim()];
  return slug ? ICONS_BY_SLUG[slug] ?? null : null;
}
