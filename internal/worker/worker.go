package worker

import (
	"context"

	"github.com/algonode/voibot/internal/algodapi"
	"github.com/algonode/voibot/internal/avapi"
	"github.com/algonode/voibot/internal/config"
	"github.com/sirupsen/logrus"
)

type Worker interface {
	Spawn(ctx context.Context) error
	Config(ctx context.Context) error
}

type WorkerAPIs struct {
	AVapi *avapi.AVAPI
	Aapi  *algodapi.AlgodAPI
}

type WorkerCommon struct {
	syncWorker bool
	cfg        *config.BotConfig
	apis       *WorkerAPIs
	log        *logrus.Entry
	realtime   bool
}

func (w *WorkerCommon) Config(ctx context.Context) error {
	w.log.Panic("Abstract worker called")
	return nil
}

func (w *WorkerCommon) Spawn(ctx context.Context) error {
	w.log.Panic("Abstract worker called")
	return nil
}
