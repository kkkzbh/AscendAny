DO $preflight$
BEGIN
    IF current_database() <> 'ascendany_v2' THEN
        RAISE EXCEPTION 'achievement rules migration requires database ascendany_v2';
    END IF;
    IF current_user <> 'ascendany_owner' THEN
        RAISE EXCEPTION 'achievement rules migration requires current role ascendany_owner';
    END IF;
    IF NOT EXISTS (
        SELECT 1
        FROM ascendany.schema_migrations_v2
        WHERE version = 3
          AND name = 'recommendation_trainer_transport'
    ) THEN
        RAISE EXCEPTION 'achievement rules migration requires schema version 3';
    END IF;
END
$preflight$;

CREATE TABLE ascendany.achievement_rule_sets (
    achievement_rule_set_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    version bigint NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT achievement_rule_sets_version_positive CHECK (version > 0)
);

CREATE TABLE ascendany.achievement_rules (
    achievement_rule_set_id bigint NOT NULL,
    achievement_code text NOT NULL,
    title text NOT NULL,
    description text NOT NULL,
    progress_key text NOT NULL,
    bronze_target numeric NOT NULL,
    silver_target numeric NOT NULL,
    gold_target numeric NOT NULL,
    sort_order bigint NOT NULL,
    PRIMARY KEY (achievement_rule_set_id, achievement_code),
    CONSTRAINT achievement_rules_rule_set_fk FOREIGN KEY (achievement_rule_set_id)
        REFERENCES ascendany.achievement_rule_sets (achievement_rule_set_id) ON DELETE RESTRICT,
    CONSTRAINT achievement_rules_code_format CHECK (
        achievement_code ~ '^[a-z][a-z0-9_]{0,63}$'
    ),
    CONSTRAINT achievement_rules_title_format CHECK (
        title = btrim(title)
        AND title <> ''
        AND octet_length(title) <= 256
    ),
    CONSTRAINT achievement_rules_description_format CHECK (
        description = btrim(description)
        AND description <> ''
        AND octet_length(description) <= 2048
    ),
    CONSTRAINT achievement_rules_progress_key_valid CHECK (
        progress_key IN (
            'exam_count',
            'positive_delta_count',
            'best_positive_streak',
            'knowledge_max',
            'accuracy_max',
            'quality_max',
            'flexibility_max',
            'proficiency_max',
            'max_rating',
            'max_rating_delta',
            'top10_count',
            'top3_count',
            'rank1_count',
            'max_of_exam_min_metric',
            'current_min_metric',
            'ai_dialogue_count'
        )
    ),
    CONSTRAINT achievement_rules_targets_positive CHECK (
        bronze_target > 0
        AND silver_target > 0
        AND gold_target > 0
        AND bronze_target < 'Infinity'::numeric
        AND silver_target < 'Infinity'::numeric
        AND gold_target < 'Infinity'::numeric
    ),
    CONSTRAINT achievement_rules_targets_ordered CHECK (
        bronze_target <= silver_target
        AND silver_target <= gold_target
    ),
    CONSTRAINT achievement_rules_sort_order_positive CHECK (sort_order > 0),
    CONSTRAINT achievement_rules_rule_set_order_unique UNIQUE (
        achievement_rule_set_id,
        sort_order
    )
);

CREATE TABLE ascendany.achievement_rule_head (
    singleton boolean PRIMARY KEY DEFAULT true,
    current_rule_set_id bigint NOT NULL UNIQUE,
    head_revision bigint NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT achievement_rule_head_singleton CHECK (singleton),
    CONSTRAINT achievement_rule_head_rule_set_fk FOREIGN KEY (current_rule_set_id)
        REFERENCES ascendany.achievement_rule_sets (achievement_rule_set_id) ON DELETE RESTRICT,
    CONSTRAINT achievement_rule_head_revision_positive CHECK (head_revision > 0)
);

CREATE FUNCTION ascendany.enforce_achievement_rule_head_transition()
RETURNS trigger
LANGUAGE plpgsql
AS $function$
DECLARE
    previous_version bigint;
    next_version bigint;
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'achievement rule head is permanent'
            USING ERRCODE = '55000';
    END IF;
    IF TG_OP = 'INSERT' THEN
        IF NEW.singleton IS DISTINCT FROM true OR NEW.head_revision <> 1 THEN
            RAISE EXCEPTION 'achievement rule head must start at revision 1'
                USING ERRCODE = '23514';
        END IF;
        RETURN NEW;
    END IF;
    IF NEW.singleton IS DISTINCT FROM OLD.singleton
       OR NEW.current_rule_set_id = OLD.current_rule_set_id
       OR NEW.head_revision <> OLD.head_revision + 1
       OR NEW.updated_at <= OLD.updated_at THEN
        RAISE EXCEPTION 'achievement rule head must advance exactly one revision'
            USING ERRCODE = '40001';
    END IF;

    SELECT version
    INTO STRICT previous_version
    FROM ascendany.achievement_rule_sets
    WHERE achievement_rule_set_id = OLD.current_rule_set_id;

    SELECT version
    INTO STRICT next_version
    FROM ascendany.achievement_rule_sets
    WHERE achievement_rule_set_id = NEW.current_rule_set_id;

    IF next_version <= previous_version THEN
        RAISE EXCEPTION 'achievement rule set version must advance monotonically'
            USING ERRCODE = '40001';
    END IF;
    RETURN NEW;
END
$function$;

REVOKE ALL ON FUNCTION ascendany.enforce_achievement_rule_head_transition() FROM PUBLIC;

CREATE TRIGGER achievement_rule_head_transition
BEFORE INSERT OR UPDATE OR DELETE ON ascendany.achievement_rule_head
FOR EACH ROW EXECUTE FUNCTION ascendany.enforce_achievement_rule_head_transition();

CREATE TRIGGER achievement_rule_head_immutable_truncate
BEFORE TRUNCATE ON ascendany.achievement_rule_head
FOR EACH STATEMENT EXECUTE FUNCTION ascendany.reject_immutable_mutation();

CREATE TRIGGER achievement_rule_sets_immutable_rows
BEFORE UPDATE OR DELETE ON ascendany.achievement_rule_sets
FOR EACH ROW EXECUTE FUNCTION ascendany.reject_immutable_mutation();

CREATE TRIGGER achievement_rule_sets_immutable_truncate
BEFORE TRUNCATE ON ascendany.achievement_rule_sets
FOR EACH STATEMENT EXECUTE FUNCTION ascendany.reject_immutable_mutation();

CREATE TRIGGER achievement_rules_immutable_rows
BEFORE UPDATE OR DELETE ON ascendany.achievement_rules
FOR EACH ROW EXECUTE FUNCTION ascendany.reject_immutable_mutation();

CREATE TRIGGER achievement_rules_immutable_truncate
BEFORE TRUNCATE ON ascendany.achievement_rules
FOR EACH STATEMENT EXECUTE FUNCTION ascendany.reject_immutable_mutation();

INSERT INTO ascendany.achievement_rule_sets (version)
VALUES (1);

INSERT INTO ascendany.achievement_rules (
    achievement_rule_set_id,
    achievement_code,
    title,
    description,
    progress_key,
    bronze_target,
    silver_target,
    gold_target,
    sort_order
)
SELECT
    rule_set.achievement_rule_set_id,
    seed.achievement_code,
    seed.title,
    seed.description,
    seed.progress_key,
    seed.bronze_target,
    seed.silver_target,
    seed.gold_target,
    seed.sort_order
FROM ascendany.achievement_rule_sets AS rule_set
CROSS JOIN (VALUES
    ('exam_count_first', '初试锋芒', '累计参赛次数达到 1 / 3 / 8 场。', 'exam_count', 1::numeric, 3::numeric, 8::numeric, 1::bigint),
    ('exam_count_veteran', '久经赛场', '累计参赛次数达到 5 / 12 / 20 场。', 'exam_count', 5::numeric, 12::numeric, 20::numeric, 2::bigint),
    ('positive_delta_count', '首次上分', 'rating 正增长次数达到 1 / 3 / 8 次。', 'positive_delta_count', 1::numeric, 3::numeric, 8::numeric, 3::bigint),
    ('best_positive_streak', '稳定连涨', '最佳连涨场次达到 2 / 4 / 6 场。', 'best_positive_streak', 2::numeric, 4::numeric, 6::numeric, 4::bigint),
    ('ai_dialogue_count', 'AI陪练', '与 AI 成功对话次数达到 3 / 15 / 40 次。', 'ai_dialogue_count', 3::numeric, 15::numeric, 40::numeric, 5::bigint),
    ('knowledge_max', '知识进阶', '知识单维最高分达到 60 / 75 / 90。', 'knowledge_max', 60::numeric, 75::numeric, 90::numeric, 6::bigint),
    ('accuracy_max', '准确进阶', '准确单维最高分达到 60 / 75 / 90。', 'accuracy_max', 60::numeric, 75::numeric, 90::numeric, 7::bigint),
    ('quality_max', '质量进阶', '质量单维最高分达到 60 / 75 / 90。', 'quality_max', 60::numeric, 75::numeric, 90::numeric, 8::bigint),
    ('flexibility_max', '灵活进阶', '灵活单维最高分达到 60 / 75 / 90。', 'flexibility_max', 60::numeric, 75::numeric, 90::numeric, 9::bigint),
    ('proficiency_max', '熟练进阶', '熟练单维最高分达到 60 / 75 / 90。', 'proficiency_max', 60::numeric, 75::numeric, 90::numeric, 10::bigint),
    ('max_rating', '评级起飞', '历史最高 rating 达到 900 / 1000 / 1200。', 'max_rating', 900::numeric, 1000::numeric, 1200::numeric, 11::bigint),
    ('max_rating_delta', '单场爆发', '历史单场涨分达到 15 / 30 / 50。', 'max_rating_delta', 15::numeric, 30::numeric, 50::numeric, 12::bigint),
    ('top10_count', '前十常客', '排名前十次数达到 1 / 3 / 6 次。', 'top10_count', 1::numeric, 3::numeric, 6::numeric, 13::bigint),
    ('top3_count', '三甲选手', '排名前三次数达到 1 / 2 / 4 次。', 'top3_count', 1::numeric, 2::numeric, 4::numeric, 14::bigint),
    ('max_of_exam_min_metric', '均衡发展', '单场五维最低分的历史最高值达到 55 / 65 / 75。', 'max_of_exam_min_metric', 55::numeric, 65::numeric, 75::numeric, 15::bigint),
    ('rank1_count', '冠军时刻', '总排名第 1 次数达到 1 / 2 / 3 次。', 'rank1_count', 1::numeric, 2::numeric, 3::numeric, 17::bigint),
    ('current_min_metric', '全能王者', '当前五维最低分达到 70 / 80 / 90。', 'current_min_metric', 70::numeric, 80::numeric, 90::numeric, 18::bigint)
) AS seed (
    achievement_code,
    title,
    description,
    progress_key,
    bronze_target,
    silver_target,
    gold_target,
    sort_order
)
WHERE rule_set.version = 1;

INSERT INTO ascendany.achievement_rule_head (
    singleton,
    current_rule_set_id,
    head_revision
)
SELECT true, achievement_rule_set_id, 1
FROM ascendany.achievement_rule_sets
WHERE version = 1;

REVOKE ALL PRIVILEGES ON TABLE
    ascendany.achievement_rule_sets,
    ascendany.achievement_rules,
    ascendany.achievement_rule_head
FROM ascendany_runtime, ascendany_backup, PUBLIC;

GRANT SELECT ON TABLE
    ascendany.achievement_rule_sets,
    ascendany.achievement_rules,
    ascendany.achievement_rule_head
TO ascendany_runtime, ascendany_backup;

REVOKE ALL PRIVILEGES ON SEQUENCE ascendany.achievement_rule_sets_achievement_rule_set_id_seq
FROM ascendany_runtime, ascendany_backup, PUBLIC;

GRANT SELECT ON SEQUENCE ascendany.achievement_rule_sets_achievement_rule_set_id_seq
TO ascendany_backup;
