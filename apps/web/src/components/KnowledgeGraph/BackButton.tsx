import React from 'react';
import * as d3 from 'd3';

interface BackButtonProps {
  staticGroupRef: React.RefObject<SVGGElement | null>;
  selectedTagId: string | null;
  showSubTags: boolean;
  returnToMainView: () => void;
  width: number;
  transitionDuration: number;
}

/**
 * 返回按钮组件，在子标签视图中显示返回主视图的按钮
 */
const BackButton: React.FC<BackButtonProps> = ({
  staticGroupRef,
  selectedTagId,
  showSubTags,
  returnToMainView,
  width,
  transitionDuration
}) => {
  // 绘制返回按钮
  React.useEffect(() => {
    if (!staticGroupRef.current || !selectedTagId) return;

    const staticGroup = d3.select(staticGroupRef.current);

    // 检查返回按钮是否已存在
    let backButton = staticGroup.select<SVGGElement>('.back-button-group');

    // 如果按钮不存在，则创建它
    if (backButton.empty()) {
      backButton = staticGroup.append('g')
        .attr('class', 'back-button-group')
        .attr('transform', `translate(${width - 80}, 40)`)
        .style('cursor', 'pointer');

      backButton.append('rect')
        .attr('width', 60)
        .attr('height', 30)
        .attr('rx', 15)
        .attr('fill', '#555555')
        .attr('stroke', '#ffffff')
        .attr('stroke-width', 1);

      backButton.append('text')
        .attr('x', 30)
        .attr('y', 15)
        .attr('text-anchor', 'middle')
        .attr('dy', '0.3em')
        .attr('fill', '#ffffff')
        .attr('font-size', '12px')
        .text('返回');
    }

    // 更新按钮状态和点击事件处理
    backButton
      .style('opacity', showSubTags ? 1 : 0)
      .style('pointer-events', showSubTags ? 'all' : 'none')
      .on('click', returnToMainView);

    // 清理函数 - 在selectedTagId为null时移除按钮
    return () => {
      if (!selectedTagId) {
        staticGroup.select('.back-button-group').remove();
      }
    };
  }, [staticGroupRef, selectedTagId, showSubTags, width, returnToMainView, transitionDuration]);

  return null; // 这个组件不直接渲染内容，只控制D3的按钮
};

export default BackButton;
