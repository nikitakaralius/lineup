package handlers

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/nikitkaralius/lineup/internal/llm"
	"github.com/nikitkaralius/lineup/internal/polls"
	"github.com/nikitkaralius/lineup/internal/queue"
	"github.com/nikitkaralius/lineup/internal/utils"
)

// formatPollTopic formats the poll topic with end time in the specified format.
func formatPollTopic(topic string, endTimeMoscow string) string {
	return fmt.Sprintf("📋 Тема: %s\n⏰ Завершится: %s", topic, endTimeMoscow)
}

func HandleMessage(
	ctx context.Context,
	bot *tgbotapi.BotAPI,
	pollsRepo *polls.Repository,
	msg *tgbotapi.Message,
	botUsername string,
	pollsService polls.Service,
	llmClient *llm.Client,
	queueService *queue.Service,
) {
	if msg.Chat == nil || (msg.Chat.Type != "group" && msg.Chat.Type != "supergroup") {
		return
	}
	text := msg.Text
	if text == "" {
		return
	}

	// Check if this is a reply to a results message (queue join/leave)
	if msg.ReplyToMessage != nil {
		// Find poll by results_message_id
		poll, err := pollsRepo.FindPollByResultsMessageID(ctx, msg.ReplyToMessage.MessageID)
		if err == nil && poll != nil {
			// This is a reply to a results message - handle queue operation
			handleQueueOperation(ctx, bot, queueService, poll.PollID, msg)
			return
		}
	}

	// Trigger on /poll command or mention of bot username
	triggered := false
	if msg.IsCommand() && msg.Command() == "poll" {
		triggered = true
		text = msg.CommandArguments()
	} else if len(msg.Entities) > 0 {
		for _, e := range msg.Entities {
			if e.Type == "mention" {
				mention := msg.Text[e.Offset : e.Offset+e.Length]
				if mention == "@"+botUsername {
					triggered = true
					// Strip mention from text
					text = msg.Text[e.Offset+e.Length:]
					break
				}
			}
		}
	}
	if !triggered {
		return
	}

	// Try LLM parsing first
	intent, err := llmClient.ParsePollIntent(ctx, text)
	if err != nil {
		// Fallback to simple parsing
		log.Printf("LLM parsing failed, using fallback: %v", err)
		topic, dur, err2 := parseTopicAndDuration(text)
		if err2 != nil {
			// Send LLM error message to user
			reply := tgbotapi.NewMessage(msg.Chat.ID, err.Error())
			reply.ReplyToMessageID = msg.MessageID
			bot.Send(reply)
			return
		}
		// Use fallback values
		intent = &llm.PollIntent{
			Topic:             topic,
			Duration:          dur.String(),
			Answers:           polls.DefaultPollAnswers,
			ComingAnswerIndex: polls.DefaultComingAnswerIndex,
		}
	}

	// Parse end time or duration
	var endsAtUTC time.Time
	var dur time.Duration

	if intent.EndTime != "" {
		// Parse end time
		endsAtUTC, err = utils.ParseEndTimeInMoscow(intent.EndTime)
		if err != nil {
			reply := tgbotapi.NewMessage(msg.Chat.ID, fmt.Sprintf("Ошибка обработки времени окончания: %v", err))
			reply.ReplyToMessageID = msg.MessageID
			bot.Send(reply)
			return
		}
		dur = endsAtUTC.Sub(time.Now().UTC())
	} else if intent.Duration != "" {
		// Parse duration
		dur, endsAtUTC, err = utils.ParseDurationInMoscow(intent.Duration)
		if err != nil {
			reply := tgbotapi.NewMessage(msg.Chat.ID, fmt.Sprintf("Ошибка обработки длительности: %v", err))
			reply.ReplyToMessageID = msg.MessageID
			bot.Send(reply)
			return
		}
	} else {
		reply := tgbotapi.NewMessage(msg.Chat.ID, "Не указана длительность или время окончания опроса")
		reply.ReplyToMessageID = msg.MessageID
		bot.Send(reply)
		return
	}

	// Format end time in Moscow timezone for display
	endTimeMoscow := utils.FormatMoscowTimeForPoll(endsAtUTC)

	// Format topic with end time
	topicWithEndTime := formatPollTopic(intent.Topic, endTimeMoscow)

	// Create poll with custom answers if specified
	answers := intent.Answers
	if len(answers) == 0 {
		answers = polls.DefaultPollAnswers
	}

	pollCfg := tgbotapi.NewPoll(msg.Chat.ID, topicWithEndTime, answers...)
	pollCfg.IsAnonymous = false
	pollCfg.AllowsMultipleAnswers = false
	sent, err := bot.Send(pollCfg)
	if err != nil {
		log.Printf("send poll error: %v", err)
		return
	}
	if sent.Poll == nil {
		log.Printf("poll send returned no poll")
		return
	}

	p := &polls.TelegramPollDTO{
		PollID:          sent.Poll.ID,
		ChatID:          msg.Chat.ID,
		MessageID:       sent.MessageID,
		Topic:           topicWithEndTime, // Store topic with end time
		CreatorID:       msg.From.ID,
		CreatorUsername: msg.From.UserName,
		CreatorName: msg.From.FirstName + func() string {
			if msg.From.LastName != "" {
				return " " + msg.From.LastName
			}
			return ""
		}(),
		StartedAt:         time.Now().UTC(),
		Duration:          dur,
		EndsAt:            endsAtUTC,
		Answers:           answers,
		ComingAnswerIndex: intent.ComingAnswerIndex,
	}

	if err := pollsRepo.InsertPoll(ctx, p); err != nil {
		log.Printf("insert poll error: %v", err)
		return
	}

	// Enqueue async job to finalize poll at EndsAt
	if pollsService != nil {
		args := polls.FinishPollArgs{PollID: p.PollID, ChatID: p.ChatID, MessageID: p.MessageID, Topic: p.Topic}
		if err := pollsService.SchedulePollFinish(ctx, args, p.EndsAt); err != nil {
			log.Printf("enqueue finish poll error: %v", err)
		}
	}
}

func handleQueueOperation(ctx context.Context, bot *tgbotapi.BotAPI, queueService *queue.Service, pollID string, msg *tgbotapi.Message) {
	text := msg.Text
	if text == "" {
		return
	}

	// Parse intent using LLM via queue service
	intent, err := queueService.ParseQueueIntent(ctx, text)
	if err != nil {
		reply := tgbotapi.NewMessage(msg.Chat.ID, fmt.Sprintf("Не могу понять ваш запрос: %v\n\nИспользуйте: 'хочу в очередь' или 'выхожу из очереди'", err))
		reply.ReplyToMessageID = msg.MessageID
		bot.Send(reply)
		return
	}

	var errMsg error
	switch intent.Action {
	case "join":
		errMsg = queueService.JoinQueue(ctx, pollID, msg.From.ID)
	case "leave":
		errMsg = queueService.LeaveQueue(ctx, pollID, msg.From.ID)
	default:
		reply := tgbotapi.NewMessage(msg.Chat.ID, "Не могу определить действие. Используйте: 'хочу в очередь' или 'выхожу из очереди'")
		reply.ReplyToMessageID = msg.MessageID
		bot.Send(reply)
		return
	}

	if errMsg != nil {
		reply := tgbotapi.NewMessage(msg.Chat.ID, fmt.Sprintf("Ошибка: %v", errMsg))
		reply.ReplyToMessageID = msg.MessageID
		bot.Send(reply)
	}
}

func parseTopicAndDuration(s string) (string, time.Duration, error) {
	// Expect format: "Topic | 30m" or "Topic 30m"
	// We'll split on '|' first; if not present, split by last space
	raw := s
	// Trim leading/trailing spaces
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", 0, fmt.Errorf("empty input")
	}
	if i := strings.Index(raw, "|"); i >= 0 {
		topic := strings.TrimSpace(raw[:i])
		durStr := strings.TrimSpace(raw[i+1:])
		dur, err := time.ParseDuration(durStr)
		if err != nil || topic == "" {
			return "", 0, fmt.Errorf("bad format")
		}
		return topic, dur, nil
	}
	// No pipe, use last space
	lastSpace := strings.LastIndex(raw, " ")
	if lastSpace < 0 {
		return "", 0, fmt.Errorf("bad format")
	}
	topic := strings.TrimSpace(raw[:lastSpace])
	durStr := strings.TrimSpace(raw[lastSpace+1:])
	dur, err := time.ParseDuration(durStr)
	if err != nil || topic == "" {
		return "", 0, fmt.Errorf("bad format")
	}
	return topic, dur, nil
}
