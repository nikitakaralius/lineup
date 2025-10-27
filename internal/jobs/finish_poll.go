package jobs

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/nikitkaralius/lineup/internal/polls"
	"github.com/nikitkaralius/lineup/internal/voters"
	"github.com/riverqueue/river"
)

type FinishPollWorker struct {
	river.WorkerDefaults[polls.FinishPollArgs]
	polls  *polls.Repository
	voters *voters.Repository
	bot    *tgbotapi.BotAPI
}

func NewFinishPollWorker(polls *polls.Repository, voters *voters.Repository, bot *tgbotapi.BotAPI) *FinishPollWorker {
	return &FinishPollWorker{polls: polls, voters: voters, bot: bot}
}

func (w *FinishPollWorker) Work(ctx context.Context, job *river.Job[polls.FinishPollArgs]) error {
	args := job.Args
	// Stop poll in chat
	stopCfg := tgbotapi.NewStopPoll(args.ChatID, args.MessageID)
	if _, err := w.bot.Send(stopCfg); err != nil {
		log.Printf("stop poll error: %v", err)
		// keep going; maybe already stopped
	}
	vs, err := w.voters.GetComingVoters(ctx, args.PollID)
	if err != nil {
		return err
	}
	shuffleVoters(vs)
	text := formatResults(args.Topic, vs)

	// Create inline keyboard for queue management
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🙋 Присоединиться", fmt.Sprintf("queue_join:%s", args.PollID)),
			tgbotapi.NewInlineKeyboardButtonData("🚪 Выйти из очереди", fmt.Sprintf("queue_exit:%s", args.PollID)),
		),
	)

	msg := tgbotapi.NewMessage(args.ChatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = keyboard
	sent, err := w.bot.Send(msg)
	if err != nil {
		return err
	}
	if err := w.polls.MarkProcessed(ctx, args.PollID, sent.MessageID); err != nil {
		return err
	}
	if err := w.voters.InsertPollResult(ctx, args.PollID, text); err != nil {
		return err
	}
	return nil
}

func shuffleVoters(v []voters.TelegramVoterDTO) {
	for i := range v {
		j := rand.Intn(i + 1)
		v[i], v[j] = v[j], v[i]
	}
}

func formatResults(topic string, voters []voters.TelegramVoterDTO) string {
	var sb strings.Builder
	sb.WriteString("🎯 *Результаты опроса:* ")
	sb.WriteString(topic)
	sb.WriteString("\n\n")

	if len(voters) == 0 {
		sb.WriteString("😔 *Никто не участвует в опросе*\n\n")
		sb.WriteString("💡 Используйте кнопки ниже, чтобы присоединиться к очереди!")
		return sb.String()
	}

	sb.WriteString(fmt.Sprintf("👥 *Участников:* %d\n\n", len(voters)))
	sb.WriteString("🏆 *Очередь участников:*\n")

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

	sb.WriteString("\n💡 *Используйте кнопки ниже для управления очередью*")
	return sb.String()
}
