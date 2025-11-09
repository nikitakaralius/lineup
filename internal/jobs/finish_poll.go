package jobs

import (
	"context"
	"log"
	"math/rand"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/nikitkaralius/lineup/internal/polls"
	"github.com/nikitkaralius/lineup/internal/queue"
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

	// Get coming_answer_index from poll
	pollInfo, err := w.polls.GetPollInfo(ctx, args.PollID)
	if err != nil {
		return err
	}

	// Get voters who selected the "coming" answer
	vs, err := w.voters.GetComingVoters(ctx, args.PollID, pollInfo.ComingAnswerIndex)
	if err != nil {
		return err
	}

	// Shuffle voters
	shuffleVoters(vs)

	// Convert to user IDs array
	queueUserIDs := make([]int64, len(vs))
	for i, v := range vs {
		queueUserIDs[i] = v.UserID
	}

	// Get voter information from repository
	votersMap, err := w.voters.GetVotersInfo(ctx, args.PollID, queueUserIDs)
	if err != nil {
		return err
	}

	// Format queue text using shared formatter
	text := queue.FormatQueueText(args.Topic, queueUserIDs, votersMap)

	msg := tgbotapi.NewMessage(args.ChatID, text)
	sent, err := w.bot.Send(msg)
	if err != nil {
		return err
	}

	if err := w.polls.MarkProcessed(ctx, args.PollID, sent.MessageID, queueUserIDs); err != nil {
		return err
	}

	if err := w.voters.InsertPollResult(ctx, args.PollID, queueUserIDs); err != nil {
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
