package handlers

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/nikitkaralius/lineup/internal/polls"
	"github.com/nikitkaralius/lineup/internal/voters"
)

// PollCreationState represents the current state of poll creation
type PollCreationState struct {
	Step     string // "topic", "duration", "confirm"
	Topic    string
	Duration time.Duration
}

// In-memory storage for poll creation states (in production, consider using Redis or database)
var pollCreationStates = make(map[string]*PollCreationState)

func HandleCallback(
	ctx context.Context,
	bot *tgbotapi.BotAPI,
	pollsRepo *polls.Repository,
	votersRepo *voters.Repository,
	callback *tgbotapi.CallbackQuery,
	botUsername string,
	pollsService polls.Service,
) {
	if callback == nil || callback.Data == "" {
		return
	}

	data := callback.Data
	chatID := callback.Message.Chat.ID
	messageID := callback.Message.MessageID
	userID := callback.From.ID

	// Answer callback to remove loading state
	answerCallback := tgbotapi.NewCallback(callback.ID, "")
	bot.Request(answerCallback)

	switch {
	case data == "create_poll":
		handleStartPollCreation(ctx, bot, chatID, messageID, userID)
	case strings.HasPrefix(data, "poll_duration:"):
		handleDurationSelection(ctx, bot, pollsRepo, chatID, messageID, userID, data, pollsService)
	case data == "poll_confirm":
		handleConfirmPoll(ctx, bot, pollsRepo, chatID, messageID, userID, pollsService)
	case data == "poll_back":
		handleBackToPollCreation(ctx, bot, chatID, messageID, userID)
	case data == "poll_cancel":
		handleCancelPollCreation(ctx, bot, chatID, messageID, userID)
	case strings.HasPrefix(data, "queue_exit:"):
		handleQueueExit(ctx, bot, pollsRepo, votersRepo, callback, data)
	case strings.HasPrefix(data, "queue_join:"):
		handleQueueJoin(ctx, bot, pollsRepo, votersRepo, callback, data)
	default:
		log.Printf("Unknown callback data: %s", data)
	}
}

func handleStartPollCreation(ctx context.Context, bot *tgbotapi.BotAPI, chatID int64, messageID int, userID int64) {
	stateKey := fmt.Sprintf("%d_%d", chatID, userID)
	pollCreationStates[stateKey] = &PollCreationState{Step: "topic"}

	text := "📝 *Создание опроса*\n\nВведите тему опроса:"
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("❌ Отмена", "poll_cancel"),
		),
	)

	edit := tgbotapi.NewEditMessageText(chatID, messageID, text)
	edit.ParseMode = "Markdown"
	edit.ReplyMarkup = &keyboard
	bot.Send(edit)
}

func handleDurationSelection(ctx context.Context, bot *tgbotapi.BotAPI, pollsRepo *polls.Repository, chatID int64, messageID int, userID int64, data string, pollsService polls.Service) {
	stateKey := fmt.Sprintf("%d_%d", chatID, userID)
	state, exists := pollCreationStates[stateKey]
	if !exists || state.Step != "duration" {
		return
	}

	// Extract duration from callback data
	parts := strings.Split(data, ":")
	if len(parts) != 2 {
		return
	}

	durationStr := parts[1]
	duration, err := time.ParseDuration(durationStr)
	if err != nil {
		log.Printf("Invalid duration format: %s", durationStr)
		return
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

	edit := tgbotapi.NewEditMessageText(chatID, messageID, text)
	edit.ParseMode = "Markdown"
	edit.ReplyMarkup = &keyboard
	bot.Send(edit)
}

func handleConfirmPoll(ctx context.Context, bot *tgbotapi.BotAPI, pollsRepo *polls.Repository, chatID int64, messageID int, userID int64, pollsService polls.Service) {
	stateKey := fmt.Sprintf("%d_%d", chatID, userID)
	state, exists := pollCreationStates[stateKey]
	if !exists || state.Step != "confirm" {
		return
	}

	// Create poll with Russian options
	pollCfg := tgbotapi.NewPoll(chatID, state.Topic, []string{"участвую", "не участвую"}...)
	pollCfg.IsAnonymous = false
	pollCfg.AllowsMultipleAnswers = false
	sent, err := bot.Send(pollCfg)
	if err != nil {
		log.Printf("send poll error: %v", err)
		// Show error message
		text := "❌ Ошибка при создании опроса. Попробуйте позже."
		edit := tgbotapi.NewEditMessageText(chatID, messageID, text)
		edit.ReplyMarkup = &tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}}
		bot.Send(edit)
		delete(pollCreationStates, stateKey)
		return
	}
	if sent.Poll == nil {
		log.Printf("poll send returned no poll")
		return
	}

	// Store poll in database
	p := &polls.TelegramPollDTO{
		PollID:          sent.Poll.ID,
		ChatID:          chatID,
		MessageID:       sent.MessageID,
		Topic:           state.Topic,
		CreatorID:       userID,
		CreatorUsername: "", // Will be filled from callback.From if available
		CreatorName:     "", // Will be filled from callback.From if available
		StartedAt:       time.Now().UTC(),
		Duration:        state.Duration,
		EndsAt:          time.Now().UTC().Add(state.Duration),
	}

	if err := pollsRepo.InsertPoll(ctx, p); err != nil {
		log.Printf("insert poll error: %v", err)
	}

	// Schedule poll completion job
	if pollsService != nil {
		args := polls.FinishPollArgs{PollID: p.PollID, ChatID: p.ChatID, MessageID: p.MessageID, Topic: p.Topic}
		if err := pollsService.SchedulePollFinish(ctx, args, p.EndsAt); err != nil {
			log.Printf("enqueue finish poll error: %v", err)
		}
	}

	// Show success message
	text := fmt.Sprintf("✅ *Опрос создан успешно!*\n\n📋 **Тема:** %s\n⏰ **Длительность:** %s\n🕐 **Завершится:** %s",
		state.Topic,
		formatDuration(state.Duration),
		p.EndsAt.Format("15:04 02.01.2006"))

	edit := tgbotapi.NewEditMessageText(chatID, messageID, text)
	edit.ParseMode = "Markdown"
	edit.ReplyMarkup = &tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}}
	bot.Send(edit)

	// Clean up state
	delete(pollCreationStates, stateKey)
}

func handleBackToPollCreation(ctx context.Context, bot *tgbotapi.BotAPI, chatID int64, messageID int, userID int64) {
	stateKey := fmt.Sprintf("%d_%d", chatID, userID)
	state, exists := pollCreationStates[stateKey]
	if !exists {
		return
	}

	if state.Step == "confirm" {
		// Go back to duration selection
		state.Step = "duration"
		showDurationSelection(ctx, bot, chatID, messageID, userID, state.Topic)
	}
}

func handleCancelPollCreation(ctx context.Context, bot *tgbotapi.BotAPI, chatID int64, messageID int, userID int64) {
	stateKey := fmt.Sprintf("%d_%d", chatID, userID)
	delete(pollCreationStates, stateKey)

	text := "❌ Создание опроса отменено."
	edit := tgbotapi.NewEditMessageText(chatID, messageID, text)
	edit.ReplyMarkup = &tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}}
	bot.Send(edit)
}

func showDurationSelection(ctx context.Context, bot *tgbotapi.BotAPI, chatID int64, messageID int, userID int64, topic string) {
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

	edit := tgbotapi.NewEditMessageText(chatID, messageID, text)
	edit.ParseMode = "Markdown"
	edit.ReplyMarkup = &keyboard
	bot.Send(edit)
}

func handleQueueExit(ctx context.Context, bot *tgbotapi.BotAPI, pollsRepo *polls.Repository, votersRepo *voters.Repository, callback *tgbotapi.CallbackQuery, data string) {
	// Extract poll_id from callback data
	parts := strings.Split(data, ":")
	if len(parts) != 2 {
		return
	}
	pollID := parts[1]

	// Remove user from queue by updating their vote to "not coming" (option 1)
	err := votersRepo.UpsertVote(ctx, pollID, *callback.From, []int{1})
	if err != nil {
		log.Printf("Error removing user from queue: %v", err)
		return
	}

	// Update the results message
	updateQueueMessage(ctx, bot, pollsRepo, votersRepo, callback.Message, pollID)

	// Send confirmation
	confirmText := "🚪 Вы вышли из очереди"
	answerCallback := tgbotapi.NewCallback(callback.ID, confirmText)
	bot.Request(answerCallback)
}

func handleQueueJoin(ctx context.Context, bot *tgbotapi.BotAPI, pollsRepo *polls.Repository, votersRepo *voters.Repository, callback *tgbotapi.CallbackQuery, data string) {
	// Extract poll_id from callback data
	parts := strings.Split(data, ":")
	if len(parts) != 2 {
		return
	}
	pollID := parts[1]

	// Add user to queue by updating their vote to "coming" (option 0)
	err := votersRepo.UpsertVote(ctx, pollID, *callback.From, []int{0})
	if err != nil {
		log.Printf("Error adding user to queue: %v", err)
		return
	}

	// Update the results message
	updateQueueMessage(ctx, bot, pollsRepo, votersRepo, callback.Message, pollID)

	// Send confirmation
	confirmText := "🙋 Вы присоединились к очереди"
	answerCallback := tgbotapi.NewCallback(callback.ID, confirmText)
	bot.Request(answerCallback)
}

func updateQueueMessage(ctx context.Context, bot *tgbotapi.BotAPI, pollsRepo *polls.Repository, votersRepo *voters.Repository, message *tgbotapi.Message, pollID string) {
	// Get current voters
	voters, err := votersRepo.GetComingVoters(ctx, pollID)
	if err != nil {
		log.Printf("Error getting voters: %v", err)
		return
	}

	// Get poll topic
	topic, err := pollsRepo.GetPollTopic(ctx, pollID)
	if err != nil {
		log.Printf("Error getting poll topic: %v", err)
		topic = "Опрос" // fallback
	}

	// Format updated results
	text := formatQueueResults(topic, voters)

	// Create inline keyboard for queue management
	keyboard := createQueueKeyboard(pollID)

	edit := tgbotapi.NewEditMessageText(message.Chat.ID, message.MessageID, text)
	edit.ParseMode = "Markdown"
	edit.ReplyMarkup = &keyboard
	bot.Send(edit)
}

func createQueueKeyboard(pollID string) tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🙋 Присоединиться", fmt.Sprintf("queue_join:%s", pollID)),
			tgbotapi.NewInlineKeyboardButtonData("🚪 Выйти из очереди", fmt.Sprintf("queue_exit:%s", pollID)),
		),
	)
}

func formatQueueResults(topic string, voters []voters.TelegramVoterDTO) string {
	var sb strings.Builder
	sb.WriteString("🎯 *Результаты опроса:* ")
	sb.WriteString(topic)
	sb.WriteString("\n\n")

	if len(voters) == 0 {
		sb.WriteString("😔 Никто не участвует в опросе")
		return sb.String()
	}

	sb.WriteString(fmt.Sprintf("👥 *Участников:* %d\n\n", len(voters)))

	for i, voter := range voters {
		sb.WriteString(fmt.Sprintf("%d. ", i+1))
		if voter.Username != "" {
			sb.WriteString("@")
			sb.WriteString(voter.Username)
			if voter.Name != "" {
				sb.WriteString(" (")
				sb.WriteString(voter.Name)
				sb.WriteString(")")
			}
		} else if voter.Name != "" {
			sb.WriteString(voter.Name)
		} else {
			sb.WriteString("Аноним")
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

func formatDuration(d time.Duration) string {
	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60

	if hours > 0 && minutes > 0 {
		return fmt.Sprintf("%d ч. %d мин.", hours, minutes)
	} else if hours > 0 {
		return fmt.Sprintf("%d ч.", hours)
	} else {
		return fmt.Sprintf("%d мин.", minutes)
	}
}
