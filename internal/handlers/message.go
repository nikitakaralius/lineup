package handlers

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/nikitkaralius/lineup/internal/polls"
)

func HandleMessage(
	ctx context.Context,
	bot *tgbotapi.BotAPI,
	store *polls.Repository,
	msg *tgbotapi.Message,
	botUsername string,
	pollsService polls.Service,
) {
	if msg.Chat == nil || (msg.Chat.Type != "group" && msg.Chat.Type != "supergroup") {
		return
	}
	text := msg.Text
	if text == "" {
		return
	}

	// Check if user is in poll creation flow
	if handlePollCreationInput(ctx, bot, store, msg, pollsService) {
		return
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

	// If no arguments provided, show interactive poll creation
	if strings.TrimSpace(text) == "" {
		showInteractivePollCreation(ctx, bot, msg.Chat.ID, msg.From.ID)
		return
	}

	// Legacy support: parse old format "Topic | 30m"
	topic, dur, err := parseTopicAndDuration(text)
	if err != nil {
		reply := tgbotapi.NewMessage(msg.Chat.ID, "💡 *Создание опроса*\n\nИспользуйте команду `/poll` без параметров для интерактивного создания опроса.\n\nИли используйте старый формат: `/poll Тема | 30m`")
		reply.ParseMode = "Markdown"
		reply.ReplyToMessageID = msg.MessageID
		bot.Send(reply)
		return
	}

	// Create poll using legacy format
	createPoll(ctx, bot, store, msg, topic, dur, pollsService)
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

func handlePollCreationInput(ctx context.Context, bot *tgbotapi.BotAPI, store *polls.Repository, msg *tgbotapi.Message, pollsService polls.Service) bool {
	stateKey := fmt.Sprintf("%d_%d", msg.Chat.ID, msg.From.ID)
	state, exists := pollCreationStates[stateKey]
	if !exists {
		return false
	}

	if state.Step == "topic" {
		// User entered topic
		topic := strings.TrimSpace(msg.Text)
		if topic == "" {
			reply := tgbotapi.NewMessage(msg.Chat.ID, "❌ Тема не может быть пустой. Попробуйте ещё раз:")
			reply.ReplyToMessageID = msg.MessageID
			bot.Send(reply)
			return true
		}

		state.Topic = topic
		state.Step = "duration"

		// Show duration selection
		text := fmt.Sprintf("⏰ *Выбор длительности опроса*\n\n📋 **Тема:** %s\n\nВыберите длительность:", topic)

		keyboard := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("⏱ 15 минут", "poll_duration:15m"),
				tgbotapi.NewInlineKeyboardButtonData("⏰ 30 минут", "poll_duration:30m"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🕐 1 час", "poll_duration:1h"),
				tgbotapi.NewInlineKeyboardButtonData("🕕 2 часа", "poll_duration:2h"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🕘 4 часа", "poll_duration:4h"),
				tgbotapi.NewInlineKeyboardButtonData("🌅 12 часов", "poll_duration:12h"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("📅 1 день", "poll_duration:24h"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("❌ Отмена", "poll_cancel"),
			),
		)

		reply := tgbotapi.NewMessage(msg.Chat.ID, text)
		reply.ParseMode = "Markdown"
		reply.ReplyMarkup = keyboard
		bot.Send(reply)
		return true
	}

	return false
}

func showInteractivePollCreation(ctx context.Context, bot *tgbotapi.BotAPI, chatID int64, userID int64) {
	stateKey := fmt.Sprintf("%d_%d", chatID, userID)
	pollCreationStates[stateKey] = &PollCreationState{Step: "topic"}

	text := "📝 *Создание опроса*\n\nВведите тему опроса:"
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("❌ Отмена", "poll_cancel"),
		),
	)

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = keyboard
	bot.Send(msg)
}

func createPoll(ctx context.Context, bot *tgbotapi.BotAPI, store *polls.Repository, msg *tgbotapi.Message, topic string, dur time.Duration, pollsService polls.Service) {
	// Create poll with Russian options
	pollCfg := tgbotapi.NewPoll(msg.Chat.ID, topic, []string{"участвую", "не участвую"}...)
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
		Topic:           topic,
		CreatorID:       msg.From.ID,
		CreatorUsername: msg.From.UserName,
		CreatorName: msg.From.FirstName + func() string {
			if msg.From.LastName != "" {
				return " " + msg.From.LastName
			}
			return ""
		}(),
		StartedAt: time.Now().UTC(),
		Duration:  dur,
		EndsAt:    time.Now().UTC().Add(dur),
	}
	if err := store.InsertPoll(ctx, p); err != nil {
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

	// Send confirmation message
	confirmText := fmt.Sprintf("✅ *Опрос создан!*\n\n📋 **Тема:** %s\n⏰ **Длительность:** %s\n🕐 **Завершится:** %s",
		topic,
		formatDuration(dur),
		p.EndsAt.Format("15:04 02.01.2006"))

	confirmMsg := tgbotapi.NewMessage(msg.Chat.ID, confirmText)
	confirmMsg.ParseMode = "Markdown"
	bot.Send(confirmMsg)
}
