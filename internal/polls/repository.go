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
	_, err := s.DB.Query(ctx, `INSERT INTO polls (
		poll_id, chat_id, message_id, topic, creator_id, creator_username, creator_name, started_at, duration_seconds, ends_at, status
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'active')
	ON CONFLICT (poll_id) DO NOTHING`,
		p.PollID, p.ChatID, p.MessageID, p.Topic, p.CreatorID, p.CreatorUsername, p.CreatorName, p.StartedAt, int(p.Duration/time.Second), p.EndsAt,
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

func (s *Repository) MarkProcessed(ctx context.Context, pollID string, resultsMessageID int) error {
	_, err := s.DB.Exec(ctx, `UPDATE polls SET status='processed', processed_at=NOW(), results_message_id=$2 WHERE poll_id=$1`, pollID, resultsMessageID)
	if err != nil {
		return err
	}
	return err
}
