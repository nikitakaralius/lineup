package polls

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// This AI crap will be refactored

type Repository struct {
	DB *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{DB: db}
}

func (s *Repository) InsertPoll(ctx context.Context, p *TelegramPollDTO) error {
	answers := p.Answers
	if len(answers) == 0 {
		answers = DefaultPollAnswers
	}
	comingIndex := p.ComingAnswerIndex
	if len(answers) == len(DefaultPollAnswers) && answers[0] == DefaultPollAnswers[0] && answers[1] == DefaultPollAnswers[1] {
		comingIndex = DefaultComingAnswerIndex
	}

	_, err := s.DB.Query(ctx, `INSERT INTO polls (
		poll_id, chat_id, message_id, topic, creator_id, creator_username, creator_name, started_at, duration_seconds, ends_at, status, answers, coming_answer_index
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'active',$11,$12)
	ON CONFLICT (poll_id) DO NOTHING`,
		p.PollID, p.ChatID, p.MessageID, p.Topic, p.CreatorID, p.CreatorUsername, p.CreatorName, p.StartedAt, int(p.Duration/time.Second), p.EndsAt, answers, comingIndex,
	)
	return err
}

func (s *Repository) FindExpiredActivePolls(ctx context.Context) ([]TelegramPollDTO, error) {
	rows, err := s.DB.Query(ctx, `SELECT poll_id, chat_id, message_id, topic, ends_at FROM polls WHERE status='active' AND ends_at <= NOW()`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var res []TelegramPollDTO
	for rows.Next() {
		var p TelegramPollDTO
		if err := rows.Scan(&p.PollID, &p.ChatID, &p.MessageID, &p.Topic, &p.EndsAt); err != nil {
			return nil, err
		}
		res = append(res, p)
	}
	return res, rows.Err()
}

func (s *Repository) MarkProcessed(ctx context.Context, pollID string, resultsMessageID int, queueUserIDs []int64) error {
	_, err := s.DB.Exec(ctx, `UPDATE polls SET status='processed', processed_at=NOW(), results_message_id=$2 WHERE poll_id=$1`, pollID, resultsMessageID)
	if err != nil {
		return err
	}
	return err
}

// FindPollByResultsMessageID finds a poll by its results message ID.
func (s *Repository) FindPollByResultsMessageID(ctx context.Context, resultsMessageID int) (*TelegramPollDTO, error) {
	var p TelegramPollDTO
	err := s.DB.QueryRow(ctx, `SELECT poll_id, chat_id, message_id, topic FROM polls WHERE results_message_id=$1`, resultsMessageID).
		Scan(&p.PollID, &p.ChatID, &p.MessageID, &p.Topic)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// GetPollInfo retrieves poll information including coming_answer_index.
// Note: This method only retrieves poll_id and coming_answer_index.
func (s *Repository) GetPollInfo(ctx context.Context, pollID string) (*TelegramPollDTO, error) {
	var p TelegramPollDTO
	err := s.DB.QueryRow(ctx, `SELECT poll_id, COALESCE(coming_answer_index, 0) FROM polls WHERE poll_id=$1`, pollID).
		Scan(&p.PollID, &p.ComingAnswerIndex)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// GetPollInfoForQueue retrieves poll information needed for queue operations.
func (s *Repository) GetPollInfoForQueue(ctx context.Context, pollID string) (chatID int64, resultsMessageID int, topic string, err error) {
	err = s.DB.QueryRow(ctx, `SELECT chat_id, results_message_id, topic FROM polls WHERE poll_id=$1`, pollID).
		Scan(&chatID, &resultsMessageID, &topic)
	return
}
