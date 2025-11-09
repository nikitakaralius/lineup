ALTER TABLE polls
ADD COLUMN IF NOT EXISTS answers TEXT[] DEFAULT ARRAY['coming', 'not coming'],
ADD COLUMN IF NOT EXISTS coming_answer_index INT DEFAULT 0;

UPDATE polls
SET answers = ARRAY['coming', 'not coming'],
    coming_answer_index = 0
WHERE answers IS NULL OR coming_answer_index IS NULL;

