package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/adjust/rmq/v4"
	"github.com/dustin/go-humanize/english"
	"github.com/ericsperano/yfh/core"
	"github.com/penglongli/gin-metrics/ginmetrics"
	log "github.com/sirupsen/logrus"
)

type Worker struct {
	YFH *core.YFH
}

func NewWorker(yfh *core.YFH) *Worker {
	return &Worker{
		YFH: yfh,
	}
}

func unmarshalTask(delivery rmq.Delivery) (*core.Task, error) {
	payload := delivery.Payload()
	var task core.Task
	if err := json.Unmarshal([]byte(payload), &task); err != nil {
		if err := delivery.Reject(); err != nil {
			return nil, fmt.Errorf("failed to reject %s: %w", payload, err)
		}
		return nil, err
	}
	log.Infof("Consuming %s", task.String())
	ginmetrics.GetMonitor().GetMetric(core.MetricTaskConsumed).Inc([]string{task.Type})
	return &task, nil
}

func getDateParam(task *core.Task) (time.Time, error) {
	return core.ParseShortTimestamp(task.Data[core.TaskDataDate])
}

func (w *Worker) Consume(delivery rmq.Delivery) {
	task, err := unmarshalTask(delivery)
	if err != nil {
		log.Error(err)
		return
	}
	ctx := context.WithValue(context.Background(), core.CtxUser, task.Data[core.TaskDataUser])
	startTime := time.Now()
	switch task.Type {
	case core.TaskInitMeta:
		err = HandleInitMeta(ctx, w.YFH.GormDB)
	case core.TaskImportFantasyGame:
		err = HandleImportFantasyGame(ctx, w.YFH)
	case core.TaskImportLeague:
		err = HandleImportLeague(ctx, w.YFH)
	case core.TaskImportTeams:
		err = HandleImportTeams(ctx, w.YFH)
	case core.TaskImportTeam:
		var teamID int
		teamID, err = strconv.Atoi(task.Data[core.TaskDataTeamID])
		if err == nil {
			err = HandleImportTeam(ctx, w.YFH, uint(teamID))
		}
	case core.TaskImportGames:
		var date time.Time
		date, err = getDateParam(task)
		if err == nil {
			err = HandleImportGames(ctx, w.YFH, date)
		}
	case core.TaskImportGame:
		var date time.Time
		date, err = getDateParam(task)
		if err == nil {
			err = HandleImportGame(ctx, w.YFH, date, task.Data[core.TaskDataGameLink])
		}
	case core.TaskImportRoster:
		var teamID int
		teamID, err = strconv.Atoi(task.Data[core.TaskDataTeamID])
		if err == nil {
			var date time.Time
			date, err = getDateParam(task)
			if err == nil {
				err = HandleImportRoster(ctx, w.YFH, uint(teamID), date)
			}
		}
	case core.TaskImportRosters:
		var date time.Time
		date, err = getDateParam(task)
		if err == nil {
			err = HandleImportRosters(ctx, w.YFH, date)
		}
	case core.TaskImportTeamSummary:
		var teamID int
		teamID, err = strconv.Atoi(task.Data[core.TaskDataTeamID])
		if err == nil {
			var date time.Time
			date, err = getDateParam(task)
			if err == nil {
				err = HandleImportTeamSummary(ctx, w.YFH, uint(teamID), date)
			}
		}
	case core.TaskImportTeamSummaries:
		var date time.Time
		date, err = getDateParam(task)
		if err == nil {
			err = HandleImportTeamSummaries(ctx, w.YFH, date)
		}
	case core.TaskImportAll:
		err = HandleImportAll(ctx, w.YFH)
	case core.TaskComputeForTeam:
		var teamID int
		teamID, err = strconv.Atoi(task.Data[core.TaskDataTeamID])
		if err == nil {
			err = HandleComputeForTeam(ctx, w.YFH, uint(teamID))
		}
	default:
		log.Warnf("No handler for %s", task.String())
		// TODO metrics for this error
	}
	endTime := time.Now()
	elapseMS := endTime.Sub(startTime).Milliseconds()
	log.Infof("Worker spent %s", english.Plural(int(elapseMS), "millisecond", ""))
	if err := ginmetrics.GetMonitor().GetMetric(core.MetricElapseTime).Add(nil, float64(elapseMS)); err != nil {
		log.Fatal(err)
	}
	if err != nil {
		log.Errorf("%s: %s", task.Type, err)
	}
	if err := delivery.Ack(); err != nil {
		log.Errorf("Failed to ack %s: %s", task.String(), err)
	} else {
		log.Debugf("Acked %s", task.String())
	}
}

func Download(ctx context.Context, yfh *core.YFH, url string) ([]byte, error) {
	client, err := NewHTTPClient(ctx, yfh)
	if err != nil {
		return nil, err
	}
	data, err := client.Download(url)
	ginmetrics.GetMonitor().GetMetric(core.MetricDownloaded).Inc([]string{url})
	return data, err
}

func StarWorkerQueue(yfh *core.YFH) error {
	const (
		prefetchLimit = 1
		pollDuration  = 100 * time.Millisecond
	)

	go logErrors(yfh.ErrChan)
	log.Infof("Worker queue started")
	if err := yfh.Queue.StartConsuming(prefetchLimit, pollDuration); err != nil {
		return err
	}
	if _, err := yfh.Queue.AddConsumer("worker", NewWorker(yfh)); err != nil {
		return err
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)
	<-signals // wait for signal
	go func() {
		<-signals // hard exit on second signal (in case shutdown gets stuck)
		os.Exit(1)
	}()
	<-yfh.RmqConnection.StopAllConsuming() // wait for all Consume() calls to finish
	log.Infof("Shutting down worker queue")
	return nil
}

func logErrors(errChan <-chan error) {
	for err := range errChan {
		switch err := err.(type) {
		case *rmq.HeartbeatError:
			if err.Count == rmq.HeartbeatErrorLimit {
				log.Errorf("heartbeat error (limit): %s", err)
			} else {
				log.Errorf("heartbeat error: %s", err)
			}
		case *rmq.ConsumeError:
			log.Errorf("Consume error: %s", err)
		case *rmq.DeliveryError:
			log.Errorf("Delivery error: %s %s", err.Delivery, err)
		default:
			log.Errorf("Other error: %s", err)
		}
	}
}
