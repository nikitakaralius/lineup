ALTER TABLE poll_results
DROP COLUMN IF EXISTS queue_user_ids,
ADD COLUMN IF EXISTS results_text TEXT;

