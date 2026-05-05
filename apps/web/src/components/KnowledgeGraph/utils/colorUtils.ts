// 颜色生成和处理相关工具函数

/**
 * 生成不同的颜色，基于HSL色彩空间，更好地区分不同标签
 * @param index 颜色索引
 * @param total 总数量
 * @returns HSL格式的颜色字符串
 */
export const generateColor = (index: number, total: number): string => {
  const hue = (index / total) * 360;
  return `hsl(${hue}, 70%, 60%)`; // 使用HSL获得更好的颜色分布
};