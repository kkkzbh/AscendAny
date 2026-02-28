CREATE TABLE IF NOT EXISTS ascendany.achievement_definitions (
    achievement_code TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    description TEXT NOT NULL,
    source TEXT NOT NULL CHECK (source IN ('ingest', 'realtime')),
    rule_type TEXT NOT NULL DEFAULT 'threshold',
    progress_key TEXT NOT NULL,
    bronze_target NUMERIC NOT NULL,
    silver_target NUMERIC NOT NULL,
    gold_target NUMERIC NOT NULL,
    sort_order INTEGER NOT NULL,
    is_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    meta JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (bronze_target <= silver_target AND silver_target <= gold_target)
);

CREATE INDEX IF NOT EXISTS achievement_definitions_source_enabled_sort_idx
    ON ascendany.achievement_definitions (source, is_enabled, sort_order);

INSERT INTO ascendany.achievement_definitions (
    achievement_code,
    title,
    description,
    source,
    rule_type,
    progress_key,
    bronze_target,
    silver_target,
    gold_target,
    sort_order,
    is_enabled
)
VALUES
    ('exam_count_first', '初试锋芒', '累计参赛次数达到 1 / 3 / 8 场。', 'ingest', 'threshold', 'exam_count', 1, 3, 8, 1, TRUE),
    ('exam_count_veteran', '久经赛场', '累计参赛次数达到 5 / 12 / 20 场。', 'ingest', 'threshold', 'exam_count', 5, 12, 20, 2, TRUE),
    ('positive_delta_count', '首次上分', 'rating 正增长次数达到 1 / 3 / 8 次。', 'ingest', 'threshold', 'positive_delta_count', 1, 3, 8, 3, TRUE),
    ('best_positive_streak', '稳定连涨', '最佳连涨场次达到 2 / 4 / 6 场。', 'ingest', 'threshold', 'best_positive_streak', 2, 4, 6, 4, TRUE),
    ('ai_dialogue_count', 'AI陪练', '与 AI 成功对话次数达到 3 / 15 / 40 次。', 'realtime', 'threshold', 'ai_dialogue_count', 3, 15, 40, 5, TRUE),
    ('knowledge_max', '知识进阶', '知识单维最高分达到 60 / 75 / 90。', 'ingest', 'threshold', 'knowledge_max', 60, 75, 90, 6, TRUE),
    ('accuracy_max', '准确进阶', '准确单维最高分达到 60 / 75 / 90。', 'ingest', 'threshold', 'accuracy_max', 60, 75, 90, 7, TRUE),
    ('quality_max', '质量进阶', '质量单维最高分达到 60 / 75 / 90。', 'ingest', 'threshold', 'quality_max', 60, 75, 90, 8, TRUE),
    ('flexibility_max', '灵活进阶', '灵活单维最高分达到 60 / 75 / 90。', 'ingest', 'threshold', 'flexibility_max', 60, 75, 90, 9, TRUE),
    ('proficiency_max', '熟练进阶', '熟练单维最高分达到 60 / 75 / 90。', 'ingest', 'threshold', 'proficiency_max', 60, 75, 90, 10, TRUE),
    ('max_rating', '评级起飞', '历史最高 rating 达到 900 / 1000 / 1200。', 'ingest', 'threshold', 'max_rating', 900, 1000, 1200, 11, TRUE),
    ('max_rating_delta', '单场爆发', '历史单场涨分达到 15 / 30 / 50。', 'ingest', 'threshold', 'max_rating_delta', 15, 30, 50, 12, TRUE),
    ('top10_count', '前十常客', '排名前十次数达到 1 / 3 / 6 次。', 'ingest', 'threshold', 'top10_count', 1, 3, 6, 13, TRUE),
    ('top3_count', '三甲选手', '排名前三次数达到 1 / 2 / 4 次。', 'ingest', 'threshold', 'top3_count', 1, 2, 4, 14, TRUE),
    ('max_of_exam_min_metric', '均衡发展', '单场五维最低分的历史最高值达到 55 / 65 / 75。', 'ingest', 'threshold', 'max_of_exam_min_metric', 55, 65, 75, 15, TRUE),
    ('any_metric_top1_count', '单项统治', '任一维度单场第一次数达到 1 / 3 / 6 次。', 'ingest', 'threshold', 'any_metric_top1_count', 1, 3, 6, 16, TRUE),
    ('rank1_count', '冠军时刻', '总排名第 1 次数达到 1 / 2 / 3 次。', 'ingest', 'threshold', 'rank1_count', 1, 2, 3, 17, TRUE),
    ('current_min_metric', '全能王者', '当前五维最低分达到 70 / 80 / 90。', 'ingest', 'threshold', 'current_min_metric', 70, 80, 90, 18, TRUE)
ON CONFLICT (achievement_code)
DO UPDATE SET
    title = EXCLUDED.title,
    description = EXCLUDED.description,
    source = EXCLUDED.source,
    rule_type = EXCLUDED.rule_type,
    progress_key = EXCLUDED.progress_key,
    bronze_target = EXCLUDED.bronze_target,
    silver_target = EXCLUDED.silver_target,
    gold_target = EXCLUDED.gold_target,
    sort_order = EXCLUDED.sort_order,
    is_enabled = EXCLUDED.is_enabled,
    updated_at = now();
