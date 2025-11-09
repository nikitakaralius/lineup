package polls

import "time"

type TelegramPollDTO struct {
	PollID            string
	ChatID            int64
	MessageID         int
	Topic             string
	CreatorID         int64
	CreatorUsername   string
	CreatorName       string
	StartedAt         time.Time
	Duration          time.Duration
	EndsAt            time.Time
	Answers           []string
	ComingAnswerIndex int
}
