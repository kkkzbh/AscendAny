// ConnectorUtils.ts - 用于处理图谱中的连线相关功能
import * as d3 from 'd3';

/**
 * 位置坐标接口
 */
export interface Position {
  x: number;
  y: number;
}

/**
 * 线端点接口，增强版
 */
export interface LineEndpoint {
  x1: number;
  y1: number;
  x2: number;
  y2: number;
  // 添加保存标签引用的字段，用于精确定位连线端点
  source?: {
    id: string;
    radius: number;
    x: number;
    y: number;
    // 保存源标签连接点的角度（相对于中心点的极坐标角度）
    angle?: number;
  };
  target?: {
    id: string;
    radius: number;
    x: number;
    y: number;
    // 保存目标标签连接点的角度（相对于中心点的极坐标角度）
    angle?: number;
  };
  // 保存初始方向向量，用于固定连接点
  initialUnitX?: number;
  initialUnitY?: number;
  // 是否已初始化标记
  initialized?: boolean;
  scaleK?: number; // 添加缩放因子
}

/**
 * 计算两个标签球之间的连线，并保存初始连接方向
 * @param source 源标签数据，包含位置和半径
 * @param target 目标标签数据，包含位置和半径
 * @returns 连线的两个端点坐标
 */
export const calculateConnection = (
  source: { x: number; y: number; radius: number; id: string },
  target: { x: number; y: number; radius: number; id: string }
): LineEndpoint | null => {
  if (!source || !target) return null;

  // 计算两点之间的距离和角度
  const dx = target.x - source.x;
  const dy = target.y - source.y;
  const distance = Math.sqrt(dx * dx + dy * dy);

  if (distance === 0) return null; // 避免除以零错误

  // 计算单位向量，用于确定连接点方向
  const unitX = dx / distance;
  const unitY = dy / distance;

  // 计算极坐标角度（用于后续计算固定连接点）
  const sourceAngle = Math.atan2(dy, dx);
  const targetAngle = Math.atan2(-dy, -dx);

  // 计算连线的起点和终点（在标签球边缘）
  // 为了避免边缘处的白点，将起点和终点稍微往内部偏移1像素
  const sourceRadius = source.radius;
  const targetRadius = target.radius;

  const startX = source.x + unitX * sourceRadius;
  const startY = source.y + unitY * sourceRadius;
  const endX = target.x - unitX * targetRadius;
  const endY = target.y - unitY * targetRadius;

  // 返回连线坐标，并保存标签引用信息和初始连接方向
  return {
    x1: startX,
    y1: startY,
    x2: endX,
    y2: endY,
    source: {
      id: source.id,
      radius: source.radius,
      x: source.x,
      y: source.y,
      angle: sourceAngle
    },
    target: {
      id: target.id,
      radius: target.radius,
      x: target.x,
      y: target.y,
      angle: targetAngle
    },
    initialUnitX: unitX,
    initialUnitY: unitY,
    initialized: true
  };
};

/**
 * 更新连线端点位置，确保连线始终固定在初始标签连接点
 * @param connection 现有连线数据
 * @param sourcePos 源标签当前位置
 * @param targetPos 目标标签当前位置
 * @param currentSourceRadius 源标签当前半径 (可选，考虑缩放)
 * @param currentTargetRadius 目标标签当前半径 (可选，考虑缩放)
 * @returns 更新后的连线端点
 */
export const updateConnectionEndpoints = (
  connection: LineEndpoint,
  sourcePos: Position,
  targetPos: Position,
  currentSourceRadius?: number, // 这个是基础半径
  currentTargetRadius?: number // 这个是基础半径
): LineEndpoint => {
  if (!connection.source || !connection.target) {
    return connection; // 如果没有源和目标信息，则返回原连线
  }

  // 定义 stroke width
  const CORE_TAG_STROKE_WIDTH = 2;
  const SUB_TAG_STROKE_WIDTH = 1.5;

  // 获取基础半径
  const sourceBaseRadius = currentSourceRadius !== undefined ? currentSourceRadius : connection.source.radius;
  const targetBaseRadius = currentTargetRadius !== undefined ? currentTargetRadius : connection.target.radius;

  // 判断是否为核心标签 (简化判断)
  const isSourceCore = connection.source.id.startsWith('core-'); // 假设核心ID以 core- 开头
  const isTargetCore = connection.target.id.startsWith('core-');

  // 计算最终用于连线的半径（包含stroke调整）
  const sourceRadius = sourceBaseRadius + (isSourceCore ? CORE_TAG_STROKE_WIDTH / 2 : SUB_TAG_STROKE_WIDTH / 2);
  const targetRadius = targetBaseRadius + (isTargetCore ? CORE_TAG_STROKE_WIDTH / 2 : SUB_TAG_STROKE_WIDTH / 2);

  // 如果有保存角度，使用角度来计算固定连接点
  if (connection.source.angle !== undefined && connection.target.angle !== undefined) {
    const sourceAngle = connection.source.angle;
    const targetAngle = connection.target.angle;

    // 使用调整后的半径计算新的连接点位置
    const startX = sourcePos.x + Math.cos(sourceAngle) * sourceRadius;
    const startY = sourcePos.y + Math.sin(sourceAngle) * sourceRadius;
    const endX = targetPos.x + Math.cos(targetAngle) * targetRadius;
    const endY = targetPos.y + Math.sin(targetAngle) * targetRadius;

    return {
      ...connection,
      x1: startX,
      y1: startY,
      x2: endX,
      y2: endY,
      source: { ...connection.source, x: sourcePos.x, y: sourcePos.y },
      target: { ...connection.target, x: targetPos.x, y: targetPos.y }
    };
  }

  // 如果有保存初始单位向量，使用它来计算固定连接点
  if (connection.initialUnitX !== undefined && connection.initialUnitY !== undefined && connection.initialized) {
    const unitX = connection.initialUnitX;
    const unitY = connection.initialUnitY;

    // 使用调整后的半径计算新的连接点位置
    const startX = sourcePos.x + unitX * sourceRadius;
    const startY = sourcePos.y + unitY * sourceRadius;
    const endX = targetPos.x - unitX * targetRadius;
    const endY = targetPos.y - unitY * targetRadius;

    return {
      ...connection,
      x1: startX,
      y1: startY,
      x2: endX,
      y2: endY,
      source: { ...connection.source, x: sourcePos.x, y: sourcePos.y },
      target: { ...connection.target, x: targetPos.x, y: targetPos.y }
    };
  }

  // 默认回退到动态计算连接点（兼容旧数据）
  const dx = targetPos.x - sourcePos.x;
  const dy = targetPos.y - sourcePos.y;
  const distance = Math.sqrt(dx * dx + dy * dy);

  if (distance === 0) return connection; // 避免除以零错误

  const unitX = dx / distance;
  const unitY = dy / distance;

  // 使用调整后的半径计算新的端点位置
  const startX = sourcePos.x + unitX * sourceRadius;
  const startY = sourcePos.y + unitY * sourceRadius;
  const endX = targetPos.x - unitX * targetRadius;
  const endY = targetPos.y - unitY * targetRadius;

  return {
    ...connection,
    x1: startX,
    y1: startY,
    x2: endX,
    y2: endY,
    source: { ...connection.source, x: sourcePos.x, y: sourcePos.y },
    target: { ...connection.target, x: targetPos.x, y: targetPos.y },
    initialUnitX: connection.initialized ? connection.initialUnitX : unitX,
    initialUnitY: connection.initialized ? connection.initialUnitY : unitY,
    initialized: true
  };
};

/**
 * 创建连接线
 * @param group D3选择器组
 * @param connections 连接线数据
 * @param className 连接线CSS类名
 * @param strokeColor 连接线颜色
 * @param strokeWidth 连接线宽度
 * @param strokeDashArray 连接线虚线模式
 * @param animationDuration 动画持续时间
 * @param animationDelay 动画延迟时间
 * @param animate 是否播放动画
 */
export const createConnectors = (
  group: d3.Selection<SVGGElement, unknown, null, undefined>,
  connections: LineEndpoint[],
  className: string,
  strokeColor: string = 'rgba(255, 255, 255, 0.6)',
  strokeWidth: number = 1.5,
  strokeDashArray: string | undefined = undefined,
  animationDuration: number = 300,
  animationDelay: number = 0,
  animate: boolean = true
): void => {
  // 创建或更新连接线
  group.selectAll<SVGLineElement, LineEndpoint>(`.${className}`)
    .data(connections)
    .join(
      enter => {
        const lines = enter.append('line')
          .attr('class', className)
          .attr('stroke', strokeColor)
          .attr('stroke-width', strokeWidth)
          .attr('pointer-events', 'none');

        if (strokeDashArray) {
          lines.attr('stroke-dasharray', strokeDashArray);
        }

        if (animate) {
          // 从起点开始，终点与起点相同，然后动画到目标终点
          lines
            .attr('x1', d => d.x1)
            .attr('y1', d => d.y1)
            .attr('x2', d => d.x1) // 起始时，终点与起点相同
            .attr('y2', d => d.y1)
            .style('opacity', 0) // 初始不可见
            .transition()
            .duration(animationDuration)
            .delay((d, i) => {
              void d;
              return animationDelay * i;
            })
            .attr('x2', d => d.x2) // 终点动画到目标位置
            .attr('y2', d => d.y2)
            .style('opacity', 1); // 淡入
        } else {
          // 不使用动画，直接设置最终位置
          lines
            .attr('x1', d => d.x1)
            .attr('y1', d => d.y1)
            .attr('x2', d => d.x2)
            .attr('y2', d => d.y2)
            .style('opacity', 1);
        }

        return lines;
      },
      update => {
        // 更新既有连接线的位置
        return update
          .attr('x1', d => d.x1)
          .attr('y1', d => d.y1)
          .attr('x2', d => d.x2)
          .attr('y2', d => d.y2)
          .attr('stroke', strokeColor)
          .attr('stroke-width', strokeWidth);
      }
    );
};

/**
 * 更新已存在的连接线位置 (不再需要，由createConnectors的enter/update/exit处理)
 */
// export const updateConnectorPositions = ... (移除)
