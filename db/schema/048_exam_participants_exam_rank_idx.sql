CREATE INDEX IF NOT EXISTS exam_participants_exam_rank_score_idx
    ON ascendany.exam_participants (exam_id, absent, rank, total_score DESC);
