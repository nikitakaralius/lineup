package queue

import (
	"fmt"
	"strings"

	"github.com/nikitkaralius/lineup/internal/voters"
)

// FormatQueueText formats the queue as text with user information.
// votersMap should contain user information for all userIDs in the queue.
func FormatQueueText(topic string, queueUserIDs []int64, votersMap map[int64]voters.TelegramVoterDTO) string {
	b := strings.Builder{}
	b.WriteString(topic)
	b.WriteString("\n")
	if len(queueUserIDs) == 0 {
		b.WriteString("No one is in the queue.")
		return b.String()
	}

	for i, userID := range queueUserIDs {
		voter, exists := votersMap[userID]
		if !exists {
			voter = voters.TelegramVoterDTO{
				UserID:   userID,
				Username: "",
				Name:     "Unknown",
			}
		}

		b.WriteString(fmt.Sprintf("%d. ", i+1))
		if voter.Username != "" {
			b.WriteString("@")
			b.WriteString(voter.Username)
			if voter.Name != "" {
				b.WriteString(" (")
				b.WriteString(voter.Name)
				b.WriteString(")")
			}
		} else {
			if voter.Name != "" {
				b.WriteString(voter.Name)
			} else {
				b.WriteString("Anonymous")
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}
