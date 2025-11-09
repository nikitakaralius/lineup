package queue

import (
	"context"
	"fmt"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/nikitkaralius/lineup/internal/llm"
	"github.com/nikitkaralius/lineup/internal/polls"
	"github.com/nikitkaralius/lineup/internal/voters"
)

// Service handles queue operations.
type Service struct {
	pollsRepo  *polls.Repository
	votersRepo *voters.Repository
	bot        *tgbotapi.BotAPI
	llmClient  *llm.Client
}

// NewService creates a new queue service.
func NewService(pollsRepo *polls.Repository, votersRepo *voters.Repository, bot *tgbotapi.BotAPI, llmClient *llm.Client) *Service {
	return &Service{
		pollsRepo:  pollsRepo,
		votersRepo: votersRepo,
		bot:        bot,
		llmClient:  llmClient,
	}
}

// JoinQueue adds a user to the end of the queue.
func (s *Service) JoinQueue(ctx context.Context, pollID string, userID int64) error {
	queueUserIDs, err := s.votersRepo.GetQueueUserIDs(ctx, pollID)
	if err != nil {
		return fmt.Errorf("failed to get queue: %w", err)
	}

	// Check if user is already in queue
	for _, id := range queueUserIDs {
		if id == userID {
			return fmt.Errorf("вы уже в очереди")
		}
	}

	// Add to end
	queueUserIDs = append(queueUserIDs, userID)

	if err := s.votersRepo.UpdateQueueUserIDs(ctx, pollID, queueUserIDs); err != nil {
		return fmt.Errorf("failed to update queue: %w", err)
	}

	return s.UpdateQueueMessage(ctx, pollID)
}

// LeaveQueue removes a user from the queue.
func (s *Service) LeaveQueue(ctx context.Context, pollID string, userID int64) error {
	queueUserIDs, err := s.votersRepo.GetQueueUserIDs(ctx, pollID)
	if err != nil {
		return fmt.Errorf("failed to get queue: %w", err)
	}

	// Remove user from queue
	newQueue := make([]int64, 0, len(queueUserIDs))
	found := false
	for _, id := range queueUserIDs {
		if id != userID {
			newQueue = append(newQueue, id)
		} else {
			found = true
		}
	}

	if !found {
		return fmt.Errorf("вы не в очереди")
	}

	if err := s.votersRepo.UpdateQueueUserIDs(ctx, pollID, newQueue); err != nil {
		return fmt.Errorf("failed to update queue: %w", err)
	}

	return s.UpdateQueueMessage(ctx, pollID)
}

// GetQueue retrieves the current queue order.
func (s *Service) GetQueue(ctx context.Context, pollID string) ([]int64, error) {
	return s.votersRepo.GetQueueUserIDs(ctx, pollID)
}

// ParseQueueIntent parses user intent for queue operations using LLM.
func (s *Service) ParseQueueIntent(ctx context.Context, text string) (*llm.QueueIntent, error) {
	return s.llmClient.ParseQueueIntent(ctx, text)
}

// UpdateQueueMessage regenerates and updates the result message in Telegram.
func (s *Service) UpdateQueueMessage(ctx context.Context, pollID string) error {
	// Get poll info from repository
	chatID, resultsMessageID, topic, err := s.pollsRepo.GetPollInfoForQueue(ctx, pollID)
	if err != nil {
		return fmt.Errorf("failed to get poll info: %w", err)
	}

	if resultsMessageID == 0 {
		return fmt.Errorf("results message not found")
	}

	queueUserIDs, err := s.votersRepo.GetQueueUserIDs(ctx, pollID)
	if err != nil {
		return fmt.Errorf("failed to get queue: %w", err)
	}

	// Get voter information from repository
	votersMap, err := s.votersRepo.GetVotersInfo(ctx, pollID, queueUserIDs)
	if err != nil {
		return fmt.Errorf("failed to get voters info: %w", err)
	}

	// Format queue text
	text := FormatQueueText(topic, queueUserIDs, votersMap)

	// Update message
	editMsg := tgbotapi.NewEditMessageText(chatID, resultsMessageID, text)
	_, err = s.bot.Send(editMsg)
	return err
}
