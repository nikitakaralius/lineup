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

	// Security: Ensure only the poll creator can input topic
	expectedUserID := msg.From.ID
	actualStateKey := fmt.Sprintf("%d_%d", msg.Chat.ID, expectedUserID)
	if stateKey != actualStateKey {
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

		// Update the initial poll creation message to remove cancel button
		if state.MessageID != 0 {
			updatedText := fmt.Sprintf("📝 *Создание опроса*\n\n✅ **Тема:** %s", topic)
			edit := tgbotapi.NewEditMessageText(msg.Chat.ID, state.MessageID, updatedText)
			edit.ParseMode = "Markdown"
			edit.ReplyMarkup = &tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}}
			bot.Send(edit)
		}

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

	if state.Step == "duration_custom" {
		// User entered custom duration
		durationStr := strings.TrimSpace(msg.Text)
		if durationStr == "" {
			reply := tgbotapi.NewMessage(msg.Chat.ID, "❌ Длительность не может быть пустой. Попробуйте ещё раз:")
			reply.ReplyToMessageID = msg.MessageID
			bot.Send(reply)
			return true
		}

		// Validate and parse duration
		duration, err := time.ParseDuration(durationStr)
		if err != nil {
			reply := tgbotapi.NewMessage(msg.Chat.ID, "❌ Неверный формат длительности. Используйте формат: `30m`, `2h`, `1h30m`\n\nПопробуйте ещё раз:")
			reply.ParseMode = "Markdown"
			reply.ReplyToMessageID = msg.MessageID
			bot.Send(reply)
			return true
		}

		// Check reasonable duration limits (1 minute to 7 days)
		if duration < time.Minute {
			reply := tgbotapi.NewMessage(msg.Chat.ID, "❌ Длительность слишком короткая. Минимум: 1 минута.")
			reply.ReplyToMessageID = msg.MessageID
			bot.Send(reply)
			return true
		}
		if duration > 7*24*time.Hour {
			reply := tgbotapi.NewMessage(msg.Chat.ID, "❌ Длительность слишком большая. Максимум: 7 дней.")
			reply.ReplyToMessageID = msg.MessageID
			bot.Send(reply)
			return true
		}

		state.Duration = duration
		state.Step = "confirm"

		// Show confirmation
		text := fmt.Sprintf("✅ *Подтверждение опроса*\n\n📋 **Тема:** %s\n⏰ **Длительность:** %s\n\nВсё правильно?",
			state.Topic, formatDuration(duration))

		keyboard := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("✅ Создать", "poll_confirm"),
				tgbotapi.NewInlineKeyboardButtonData("🔙 Назад", "poll_back"),
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

	text := "📝 *Создание опроса*\n\nВыберите тему опроса или введите свою:"
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📊 Анализ данных", "poll_topic:Анализ данных"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔒 Информационная безопасность", "poll_topic:Информационная безопасность"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🤖 Промпт инжениринг", "poll_topic:Промпт инжениринг"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🎨 Интерфейсы", "poll_topic:Интерфейсы"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🏛️ Сбер", "poll_topic:Сбер"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("❌ Отмена", "poll_cancel"),
		),
	)

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = keyboard
	sent, err := bot.Send(msg)
	if err != nil {
		log.Printf("Error sending poll creation message: %v", err)
		return
	}

	// Store state with message ID for later deletion
	pollCreationStates[stateKey] = &PollCreationState{Step: "topic", MessageID: sent.MessageID}
}

func createPoll(ctx context.Context, bot *tgbotapi.BotAPI, store *polls.Repository, msg *tgbotapi.Message, topic string, dur time.Duration, pollsService polls.Service) {
	// Create enhanced poll question with duration and end time
	endTime := time.Now().UTC().Add(dur)
	pollQuestion := fmt.Sprintf("📋 Тема: %s\n⏰ Длительность: %s\n🕐 Завершится: %s",
		topic,
		formatDuration(dur),
		formatTimeInMSK(endTime))

	// Create poll with Russian options
	pollCfg := tgbotapi.NewPoll(msg.Chat.ID, pollQuestion, []string{"Иду", "Не иду"}...)
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
}

func formatTimeInMSK(t time.Time) string {
	// Convert UTC time to Moscow Standard Time (UTC+3)
	msk := time.FixedZone("MSK", 3*60*60)
	return t.In(msk).Format("15:04 02.01.2006 MSK")
}
