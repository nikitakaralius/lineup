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
	msg := tgbotapi.NewMessage(args.ChatID, text)
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
	b := strings.Builder{}
	b.WriteString("Results for: ")
	b.WriteString(topic)
	b.WriteString("\n")
	if len(voters) == 0 {
		b.WriteString("No one is coming.")
		return b.String()
	}
	for i, v := range voters {
		b.WriteString(fmt.Sprintf("%d. ", i+1))
		if v.Username != "" {
			b.WriteString("@")
			b.WriteString(v.Username)
			if v.Name != "" {
				b.WriteString(" (")
				b.WriteString(v.Name)
				b.WriteString(")")
			}
		} else {
			if v.Name != "" {
				b.WriteString(v.Name)
			} else {
				b.WriteString("Anonymous")
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}
