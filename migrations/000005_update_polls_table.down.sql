ALTER TABLE polls
DROP COLUMN IF EXISTS answers,
DROP COLUMN IF EXISTS coming_answer_index;

