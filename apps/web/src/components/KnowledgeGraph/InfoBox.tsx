import React, { useRef, useEffect } from 'react';
import { TagData, InfoBoxData, RecommendedProblem } from './types';
import { Link } from 'react-router-dom';

interface InfoBoxProps {
  hoveredTag: TagData | null;
  selectedTagId: string | null;
  infoBox: InfoBoxData;
  setInfoBox: React.Dispatch<React.SetStateAction<InfoBoxData>>;
}

// 难度系数转换为文本和颜色
const getDifficultyInfo = (difficulty: number) => {
  switch(Math.round(difficulty)) {
    case 1: return { text: '入门', color: '#4CAF50' };
    case 2: return { text: '简单', color: '#8BC34A' };
    case 3: return { text: '中等', color: '#FFC107' };
    case 4: return { text: '困难', color: '#FF9800' };
    case 5: return { text: '挑战', color: '#F44336' };
    default: return { text: '未知', color: '#9E9E9E' };
  }
};

// 评分转换为色彩渐变
const getScoreColor = (score: number) => {
  if (score < 0.3) return 'rgb(239, 83, 80)'; // 红色
  if (score < 0.6) return 'rgb(255, 167, 38)'; // 橙色
  return 'rgb(102, 187, 106)'; // 绿色
};

const clamp01 = (value: number) => Math.max(0, Math.min(1, value));

/**
 * 信息框组件，在鼠标悬停时显示标签信息
 */
const InfoBox: React.FC<InfoBoxProps> = ({ hoveredTag, selectedTagId, infoBox, setInfoBox }) => {
  // 创建对信息框的引用
  const infoBoxRef = useRef<HTMLDivElement>(null);
  // 跟踪鼠标是否悬停在信息框上
  const isHoveringInfoBox = useRef(false);

  // 清理函数的引用
  const clearTimeoutRef = useRef<number | null>(null);

  // 处理组件卸载时的清理和鼠标事件状态同步
  useEffect(() => {
    return () => {
      // 清理任何可能存在的定时器
      if (clearTimeoutRef.current) {
        window.clearTimeout(clearTimeoutRef.current);
        clearTimeoutRef.current = null;
      }
    };
  }, []);

  // 处理鼠标进入信息框事件
  const handleInfoBoxMouseEnter = () => {
    isHoveringInfoBox.current = true;
    // 确保信息框保持可见
    setInfoBox(prev => ({ ...prev, opacity: 1 }));
  };

  // 处理鼠标离开信息框事件
  const handleInfoBoxMouseLeave = () => {
    isHoveringInfoBox.current = false;
    // 当鼠标离开信息框时触发淡出
    setInfoBox(prev => ({ ...prev, opacity: 0 }));
  };

  // 渲染子标签视图中的推荐题目列表
  const renderRecommendedProblems = (problems?: RecommendedProblem[]) => {
    if (!problems || problems.length === 0) {
      return <div className="no-problems">暂无推荐题目</div>;
    }

    return (
      <div className="problem-list">
        {problems.map(problem => {
          const difficultyInfo = getDifficultyInfo(problem.difficulty);
          const to = `/oj/${encodeURIComponent(problem.id)}`;
          return (
            <div key={problem.id} className="problem-item">
              <div className="problem-title">
                <Link to={to} target="_blank" rel="noopener noreferrer">{problem.title}</Link>
              </div>
              <div className="problem-meta">
                <div className="problem-tags">
                  {problem.tags.map((tag, index) => (
                    <span key={index} className="problem-tag">{tag}</span>
                  ))}
                </div>
                <div className="problem-difficulty" style={{ color: difficultyInfo.color }}>
                  {difficultyInfo.text}
                </div>
              </div>
            </div>
          );
        })}
      </div>
    );
  };

  // 渲染核心标签的简化信息框
  const renderCoreTagInfoBox = () => {
    const score = clamp01(infoBox.tagScore !== undefined ? infoBox.tagScore : 0.5);
    const isWeakPoint = infoBox.isWeakPoint ?? false;
    const isOnLearningPath = infoBox.isOnLearningPath ?? false;

    return (
      <div className="enhanced-info-box core-tag-info">
        <div className="info-header">
          <div className="info-title">
            {infoBox.tagName || infoBox.content}
          </div>
          <div className="info-badges">
            {isWeakPoint && (
              <span className="badge weak-point-badge">⚡ 薄弱点</span>
            )}
            {isOnLearningPath && (
              <span className="badge learning-path-badge">📍 学习路径</span>
            )}
          </div>
        </div>

        <div className="info-metrics">
          <div className="metric full-width">
            <div className="metric-label">掌握程度</div>
            <div className="metric-value">
              <div className="score-bar-container">
                <div
                  className="score-bar"
                  style={{
                    width: `${score * 100}%`,
                    backgroundColor: getScoreColor(score)
                  }}
                ></div>
              </div>
              <div className="score-value" style={{ color: getScoreColor(score) }}>
                {(score * 100).toFixed(0)}%
              </div>
            </div>
          </div>
        </div>

        <div className="info-hint">
          点击查看子知识点
        </div>
      </div>
    );
  };

  // 渲染子标签视图中的扩展信息框
  const renderEnhancedInfoBox = () => {
    const scoreValue = typeof infoBox.tagScore === 'number' ? clamp01(infoBox.tagScore) : null;
    const acCount = typeof infoBox.acCount === 'number' ? infoBox.acCount : null;
    const problems: RecommendedProblem[] = infoBox.recommendedProblems || [];

    return (
      <div className="enhanced-info-box">
        <div className="info-header">
          <div className="info-title">
            {/* 优先使用 tagName，如果没有 tagName 则使用 content 但去除占位符文本 */}
            {infoBox.tagName || (infoBox.content?.replace(/ - \(更多信息...\)$/, ''))}
          </div>
        </div>

        <div className="info-metrics">
          <div className="metric">
            <div className="metric-label">掌握程度</div>
            <div className="metric-value">
              <div className="score-bar-container">
                <div
                  className="score-bar"
                  style={{
                    width: `${(scoreValue ?? 0) * 100}%`,
                    backgroundColor: scoreValue === null ? 'rgba(255, 255, 255, 0.18)' : getScoreColor(scoreValue)
                  }}
                ></div>
              </div>
              <div className="score-value" style={{ color: scoreValue === null ? 'rgba(255, 255, 255, 0.65)' : getScoreColor(scoreValue) }}>
                {scoreValue === null ? '--' : scoreValue.toFixed(2)}
              </div>
            </div>
          </div>

          <div className="metric">
            <div className="metric-label">已解题数</div>
            <div className="metric-value ac-count">{acCount ?? '--'}</div>
          </div>
        </div>

        <div className="info-section">
          <div className="section-title">题目推荐</div>
          {renderRecommendedProblems(problems)}
        </div>
      </div>
    );
  };

  return (
    <>
      {/* 主视图中的悬浮信息框 */}
      {hoveredTag && !selectedTagId && (
        <div
          style={{
            position: 'absolute',
            bottom: '20px',
            left: '50%',
            transform: 'translateX(-50%)',
            backgroundColor: 'rgba(0, 0, 0, 0.7)',
            color: 'white',
            padding: '10px 15px',
            borderRadius: '5px',
            maxWidth: '300px',
            textAlign: 'center',
            backdropFilter: 'blur(3px)',
            boxShadow: '0 0 10px rgba(255, 255, 255, 0.2)',
            pointerEvents: 'none', // 确保不阻挡点击事件
          }}
        >
          <h3 style={{ margin: '0 0 5px 0' }}>{hoveredTag.name}</h3>
          <p style={{ margin: '0' }}>点击进行缩放查看</p>
        </div>
      )}

      {/* 子标签视图中的悬浮信息框 - 使用增强版样式 */}
      {infoBox.visible && (
        <div
          ref={infoBoxRef}
          onMouseEnter={handleInfoBoxMouseEnter}
          onMouseLeave={handleInfoBoxMouseLeave}
          style={{
            position: 'absolute',
            top: infoBox.y + 10,
            left: infoBox.x + 10,
            backgroundColor: 'rgba(15, 23, 42, 0.95)',
            color: 'white',
            padding: '0',
            borderRadius: '8px',
            width: '320px',
            zIndex: 1000,
            pointerEvents: 'auto', // 修改为auto，允许信息框捕获鼠标事件
            backdropFilter: 'blur(6px)',
            boxShadow: '0 4px 20px rgba(0, 0, 0, 0.5), 0 0 15px rgba(78, 78, 255, 0.3)',
            opacity: infoBox.opacity,
            transition: 'opacity 0.35s cubic-bezier(0.4, 0, 0.2, 1), transform 0.35s cubic-bezier(0.4, 0, 0.2, 1)',
            border: '1px solid rgba(78, 78, 255, 0.2)',
            overflow: 'hidden',
            transform: 'scale(' + (infoBox.opacity === 0 ? 0.95 : 1) + ')',
            transformOrigin: 'top left',
          }}
        >
          <style>
            {`
              .enhanced-info-box {
                padding: 15px;
                font-family: 'Inter', -apple-system, BlinkMacSystemFont, system-ui, sans-serif;
              }

              .info-header {
                margin-bottom: 15px;
                border-bottom: 1px solid rgba(255, 255, 255, 0.1);
                padding-bottom: 10px;
              }

              .info-title {
                font-size: 16px;
                font-weight: 600;
                color: #fff;
              }

              .info-metrics {
                display: flex;
                margin-bottom: 15px;
                gap: 20px;
              }

              .metric {
                flex: 1;
              }

              .metric-label {
                font-size: 12px;
                color: rgba(255, 255, 255, 0.7);
                margin-bottom: 5px;
              }

              .metric-value {
                font-size: 16px;
                font-weight: 600;
                display: flex;
                align-items: center;
              }

              .score-bar-container {
                height: 8px;
                width: 100%;
                background-color: rgba(255, 255, 255, 0.1);
                border-radius: 4px;
                overflow: hidden;
                margin-right: 10px;
                flex-grow: 1;
              }

              .score-bar {
                height: 100%;
                border-radius: 4px;
                transition: width 0.5s ease;
              }

              .score-value {
                min-width: 45px;
                text-align: right;
              }

              .ac-count {
                color: #64B5F6;
              }

              .info-section {
                margin-top: 15px;
              }

              .section-title {
                font-size: 14px;
                font-weight: 600;
                margin-bottom: 10px;
                color: rgba(255, 255, 255, 0.9);
              }

              .problem-list {
                max-height: 200px;
                overflow-y: auto;
                border-radius: 6px;
                background-color: rgba(0, 0, 0, 0.2);
              }

              .problem-item {
                padding: 10px;
                border-bottom: 1px solid rgba(255, 255, 255, 0.05);
              }

              .problem-item:last-child {
                border-bottom: none;
              }

              .problem-title {
                font-size: 13px;
                margin-bottom: 5px;
              }

              .problem-title a {
                color: #90CAF9;
                text-decoration: none;
              }

              .problem-meta {
                display: flex;
                justify-content: space-between;
                align-items: center;
                font-size: 12px;
              }

              .problem-tags {
                display: flex;
                gap: 5px;
                flex-wrap: wrap;
              }

              .problem-tag {
                background-color: rgba(255, 255, 255, 0.1);
                padding: 2px 6px;
                border-radius: 4px;
                color: rgba(255, 255, 255, 0.7);
              }

              .problem-difficulty {
                font-weight: 600;
              }

              .no-problems {
                padding: 15px;
                text-align: center;
                color: rgba(255, 255, 255, 0.5);
                font-style: italic;
              }

              /* 自定义滚动条 */
              .problem-list::-webkit-scrollbar {
                width: 6px;
              }

              .problem-list::-webkit-scrollbar-track {
                background: rgba(255, 255, 255, 0.05);
                border-radius: 3px;
              }

              .problem-list::-webkit-scrollbar-thumb {
                background: rgba(255, 255, 255, 0.2);
                border-radius: 3px;
              }

              .problem-list::-webkit-scrollbar-thumb:hover {
                background: rgba(255, 255, 255, 0.3);
              }
            `}
          </style>
          {infoBox.isCoreTag ? renderCoreTagInfoBox() : renderEnhancedInfoBox()}
        </div>
      )}
    </>
  );
};

export default InfoBox;
