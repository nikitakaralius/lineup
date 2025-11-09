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

func (s *Repository) GetComingVoters(ctx context.Context, pollID string, comingAnswerIndex int) ([]TelegramVoterDTO, error) {
	rows, err := s.DB.Query(ctx, `SELECT user_id, COALESCE(username,''), COALESCE(name,'') FROM poll_votes WHERE poll_id=$1 AND $2 = ANY(option_ids)`, pollID, comingAnswerIndex)
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

func (s *Repository) InsertPollResult(ctx context.Context, pollID string, queueUserIDs []int64) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO poll_results (poll_id, queue_user_ids, created_at) VALUES ($1,$2,NOW()) ON CONFLICT (poll_id) DO NOTHING`, pollID, queueUserIDs)
	return err
}

// GetQueueUserIDs retrieves the current queue user IDs for a poll.
func (s *Repository) GetQueueUserIDs(ctx context.Context, pollID string) ([]int64, error) {
	var queueUserIDs []int64
	err := s.DB.QueryRow(ctx, `SELECT queue_user_ids FROM poll_results WHERE poll_id=$1`, pollID).
		Scan(&queueUserIDs)
	if err != nil {
		return nil, err
	}
	return queueUserIDs, nil
}

// UpdateQueueUserIDs updates the queue user IDs for a poll.
func (s *Repository) UpdateQueueUserIDs(ctx context.Context, pollID string, queueUserIDs []int64) error {
	_, err := s.DB.Exec(ctx, `UPDATE poll_results SET queue_user_ids=$2 WHERE poll_id=$1`, pollID, queueUserIDs)
	return err
}

// GetVotersInfo retrieves user information for a list of user IDs for a specific poll.
func (s *Repository) GetVotersInfo(ctx context.Context, pollID string, userIDs []int64) (map[int64]TelegramVoterDTO, error) {
	if len(userIDs) == 0 {
		return make(map[int64]TelegramVoterDTO), nil
	}

	rows, err := s.DB.Query(ctx, `SELECT user_id, COALESCE(username,''), COALESCE(name,'') FROM poll_votes WHERE poll_id=$1 AND user_id = ANY($2)`, pollID, userIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[int64]TelegramVoterDTO)
	for rows.Next() {
		var v TelegramVoterDTO
		if err := rows.Scan(&v.UserID, &v.Username, &v.Name); err != nil {
			return nil, err
		}
		result[v.UserID] = v
	}

	// Fill in missing users with placeholder
	for _, userID := range userIDs {
		if _, exists := result[userID]; !exists {
			result[userID] = TelegramVoterDTO{
				UserID:   userID,
				Username: "",
				Name:     "Unknown",
			}
		}
	}

	return result, rows.Err()
}

func intSliceToArray(a []int) any {
	b := make([]int32, len(a))
	for i, v := range a {
		b[i] = int32(v)
	}
	return b
}
