package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/firebase/genkit/go/ai"
	"github.com/firebase/genkit/go/genkit"
	"github.com/firebase/genkit/go/plugins/compat_oai/openai"
	"github.com/nikitkaralius/lineup/internal/polls"
)

// Client wraps Genkit for LLM operations.
type Client struct {
	genkit *genkit.Genkit
	model  ai.Model
	openai *openai.OpenAI
}

// NewClient creates a new LLM client with Genkit and OpenAI.
func NewClient(ctx context.Context, apiKey string) (*Client, error) {
	oai := &openai.OpenAI{APIKey: apiKey}
	g := genkit.Init(ctx, genkit.WithPlugins(oai))
	model := oai.Model(g, "gpt-4o-mini")

	return &Client{
		genkit: g,
		model:  model,
		openai: oai,
	}, nil
}

// ParsePollIntent uses LLM to parse user intent for creating a poll.
// Returns structured PollIntent or an error with helpful message.
func (c *Client) ParsePollIntent(ctx context.Context, text string) (*PollIntent, error) {
	now := time.Now()
	moscowLocation, _ := time.LoadLocation("Europe/Moscow")
	nowMoscow := now.In(moscowLocation)
	currentYear := nowMoscow.Year()
	currentDate := nowMoscow.Format("2006-01-02")
	currentDateTime := nowMoscow.Format("2006-01-02 15:04")

	prompt := fmt.Sprintf(`You are a helpful assistant that parses user requests for creating polls in Russian or English.

CURRENT DATE CONTEXT: 
- Today's date in Moscow timezone: %s
- Current date and time in Moscow: %s MSK
- Current year: %d
Use this information when converting relative time references to absolute dates. When user says "15:08" or "до 15:08", they mean TODAY (%s) at 15:08 Moscow time, unless the time has already passed today (then use tomorrow).

The user wants to create a poll with:
1. Topic (required) - what the poll is about
2. End time OR Duration (at least one required):
   - End time: when the poll should end in Moscow timezone (Europe/Moscow, UTC+3). 
     You MUST convert any relative time references (like "13:48", "tomorrow 13:48", "Monday 13:48", "next week", etc.) 
     to an absolute ISO 8601 datetime string in Moscow timezone format: "%d-01-02T15:04:05+03:00"
     Examples (today is %s):
     * "13:48" or "end at 13:48" or "до 13:48" -> convert to TODAY (%s) at 13:48 Moscow time, unless 13:48 has already passed today (then use tomorrow)
     * "tomorrow 13:48" or "завтра 13:48" -> convert to tomorrow's date at 13:48 Moscow time
     * "Monday 13:48" or "понедельник 13:48" -> convert to next Monday at 13:48 Moscow time
   - Duration: how long the poll should last (e.g., "30m", "1h", "2h30m")
3. Answers (optional) - custom poll answers. If not specified, use default: ["Иду", "Не иду"]
4. Coming answer index (required if custom answers) - which answer index means "Иду" (0-based)

IMPORTANT: 
- If user specifies an end time, ALWAYS return end_time as ISO 8601 format: "%d-01-02T15:04:05+03:00" (use current year %d and today's date %s if it's just a time like "15:08")
- If user specifies duration, return duration field
- If both are specified, prefer end_time
- ALWAYS use year %d and today's date %s when converting simple times like "15:08" to absolute dates

If the user specifies custom answers, you MUST identify which one means "Иду" (going/attending). 
If you cannot determine which answer means "Иду", you must return an error message asking the user to specify explicitly.

Parse the following user input and return ONLY valid JSON in this exact format:
{
  "topic": "string",
  "duration": "string (e.g., 30m, 1h)" (optional if end_time is provided),
  "end_time": "string in ISO 8601 format: %d-01-02T15:04:05+03:00" (optional if duration is provided, MUST be in Moscow timezone, use year %d and today's date %s for simple times),
  "answers": ["string"] (optional, omit if not specified),
  "coming_answer_index": int (0-based index, required if answers are specified)
}

If you cannot parse the intent, return ONLY an error message (not JSON) explaining:
- What field is missing (topic, duration/end_time, or coming_answer_index if custom answers)
- What the user should add to their request
- Examples of correct formats

User input: `, currentDate, currentDateTime, currentYear, currentDate, currentYear, currentDate, currentDate, currentYear, currentYear, currentDate, currentYear, currentYear, currentDate) + text

	resp, err := genkit.Generate(ctx, c.genkit,
		ai.WithModel(c.model),
		ai.WithPrompt(prompt),
	)
	if err != nil {
		return nil, fmt.Errorf("LLM request failed: %w", err)
	}

	content := strings.TrimSpace(resp.Text())

	// Check if LLM returned an error message instead of JSON
	// If content doesn't start with {, it's likely an error message
	if !strings.HasPrefix(strings.TrimSpace(content), "{") {
		return nil, fmt.Errorf("%s\n\nПримеры правильного формата:\n/poll Тема | 30m\n/poll Тема | до 13:48\n/poll Тема | завтра 13:48", content)
	}

	var intent PollIntent
	if err := json.Unmarshal([]byte(content), &intent); err != nil {
		return nil, fmt.Errorf("failed to parse LLM response as JSON: %w. Response: %s", err, content)
	}

	// Validate required fields
	if intent.Topic == "" {
		return nil, fmt.Errorf("❌ Тема опроса не указана.\n\nЧто добавить: укажите тему опроса\nПримеры:\n/poll Математика | 30m\n/poll Практика | до 13:48")
	}
	if intent.Duration == "" && intent.EndTime == "" {
		return nil, fmt.Errorf("❌ Не указана длительность или время окончания опроса.\n\nЧто добавить: укажите длительность (например, 30m, 1h) или время окончания (например, до 13:48, завтра 13:48)\nПримеры:\n/poll Тема | 30m\n/poll Тема | до 13:48\n/poll Тема | завтра 13:48")
	}

	// Set defaults if answers not specified
	if len(intent.Answers) == 0 {
		intent.Answers = polls.DefaultPollAnswers
		intent.ComingAnswerIndex = polls.DefaultComingAnswerIndex
	} else {
		// Validate coming_answer_index if answers are specified
		if intent.ComingAnswerIndex < 0 || intent.ComingAnswerIndex >= len(intent.Answers) {
			return nil, fmt.Errorf("❌ Не указано, какой вариант ответа означает 'Иду'.\n\nЧто добавить: явно укажите в запросе, какой вариант означает, что человек идет\nПример: /poll Тема | 30m | Иду, Не иду (первый вариант - иду)")
		}
	}

	return &intent, nil
}

// ParseQueueIntent uses LLM to parse user intent for queue operations (join/leave).
func (c *Client) ParseQueueIntent(ctx context.Context, text string) (*QueueIntent, error) {
	prompt := `You are a helpful assistant that parses user requests in Russian or English for queue operations.

The user wants to either join or leave a queue. Parse the following text and determine the intent.

Return ONLY valid JSON in this exact format:
{
  "action": "join" or "leave"
}

Common Russian phrases:
- Join: "хочу в очередь", "добавь меня", "я иду", "запиши меня", "join", "add me"
- Leave: "выхожу из очереди", "убери меня", "я не иду", "скип", "remove me", "leave"

User input: ` + text

	resp, err := genkit.Generate(ctx, c.genkit,
		ai.WithModel(c.model),
		ai.WithPrompt(prompt),
	)
	if err != nil {
		return nil, fmt.Errorf("LLM request failed: %w", err)
	}

	content := strings.TrimSpace(resp.Text())

	var intent QueueIntent
	if err := json.Unmarshal([]byte(content), &intent); err != nil {
		return nil, fmt.Errorf("failed to parse LLM response: %w. Response: %s", err, content)
	}

	if intent.Action != "join" && intent.Action != "leave" {
		return nil, fmt.Errorf("не могу определить действие. Используйте: 'хочу в очередь' или 'выхожу из очереди'")
	}

	return &intent, nil
}
