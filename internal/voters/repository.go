package voters

import (
	"context"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// This AI crap will be refactored

type Repository struct {
	DB *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{DB: db}
}

func (s *Repository) UpsertVote(ctx context.Context, pollID string, u tgbotapi.User, optionIDs []int) error {
	name := u.FirstName
	if u.LastName != "" {
		name = name + " " + u.LastName
	}
	_, err := s.DB.Exec(ctx, `INSERT INTO poll_votes (poll_id, user_id, username, name, option_ids, updated_at)
	VALUES ($1,$2,$3,$4,$5, NOW())
	ON CONFLICT (poll_id, user_id) DO UPDATE SET username=EXCLUDED.username, name=EXCLUDED.name, option_ids=EXCLUDED.option_ids, updated_at=NOW()`,
		pollID, u.ID, u.UserName, name, intSliceToArray(optionIDs),
	)
	return err
}

func (s *Repository) GetComingVoters(ctx context.Context, pollID string) ([]TelegramVoterDTO, error) {
	// Option index 0 corresponds to "coming"
	rows, err := s.DB.Query(ctx, `SELECT user_id, COALESCE(username,''), COALESCE(name,'') FROM poll_votes WHERE poll_id=$1 AND 0 = ANY(option_ids)`, pollID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var vs []TelegramVoterDTO
	for rows.Next() {
		var v TelegramVoterDTO
		if err := rows.Scan(&v.UserID, &v.Username, &v.Name); err != nil {
			return nil, err
		}
		vs = append(vs, v)
	}
	return vs, rows.Err()
}

func (s *Repository) InsertPollResult(ctx context.Context, pollID string, resultsText string) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO poll_results (poll_id, results_text, created_at) VALUES ($1,$2,NOW()) ON CONFLICT (poll_id) DO NOTHING`, pollID, resultsText)
	return err
}

func intSliceToArray(a []int) any {
	b := make([]int32, len(a))
	for i, v := range a {
		b[i] = int32(v)
	}
	return b
}
