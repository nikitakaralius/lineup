package llm

// PollIntent represents the parsed intent from user input for creating a poll.
type PollIntent struct {
	Topic             string   `json:"topic"`
	Duration          string   `json:"duration,omitempty"`  // e.g., "30m", "1h", "2h30m" (optional if end_time is provided)
	EndTime           string   `json:"end_time,omitempty"`  // ISO 8601 format in Moscow timezone, e.g., "2024-01-15T13:48:00+03:00" or "13:48" (today), "tomorrow 13:48", "Monday 13:48"
	Answers           []string `json:"answers,omitempty"`   // Optional custom answers
	ComingAnswerIndex int      `json:"coming_answer_index"` // Index of answer that means "coming"
}

// QueueIntent represents the parsed intent for queue operations.
type QueueIntent struct {
	Action string `json:"action"` // "join" or "leave"
}
