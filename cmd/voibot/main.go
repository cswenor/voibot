// Copyright (C) 2022 AlgoNode Org.
//
// voibot is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as
// published by the Free Software Foundation, either version 3 of the
// License, or (at your option) any later version.
//
// voibot is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with voibot.  If not, see <https://www.gnu.org/licenses/>.

package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/algonode/voibot/internal/algodapi"
	"github.com/algonode/voibot/internal/avapi"
	"github.com/algonode/voibot/internal/config"
	"github.com/algonode/voibot/internal/worker"

	log "github.com/sirupsen/logrus"
)

func init() {
	log.SetFormatter(&log.JSONFormatter{})
	log.SetOutput(os.Stderr)
	log.SetLevel(log.InfoLevel)
}

func main() {

	slog := log.StandardLogger()

	//load config
	cfg, err := config.LoadConfig()
	if err != nil {
		log.WithError(err).Error()
		return
	}

	//make us a nice cancellable context
	//set Ctrl-C as the cancell trigger
	ctx, cf := context.WithCancel(context.Background())
	defer cf()
	{
		cancelCh := make(chan os.Signal, 1)
		signal.Notify(cancelCh, syscall.SIGTERM, syscall.SIGINT)
		go func() {
			<-cancelCh
			log.Error("stopping streamer")
			cf()
		}()
	}

	aapi, err := algodapi.Make(ctx, cfg.Algod, slog)
	if err != nil {
		log.Panic(err)
	}

	avapi, err := avapi.Make(ctx, cfg.AVAPI, slog)
	if err != nil {
		log.Panic(err)
	}

	apis := &worker.WorkerAPIs{
		AVapi: avapi,
		Aapi:  aapi,
	}

	workers := []worker.Worker{
		worker.EQUALIZERWorkerNew(ctx, apis, slog, &cfg),
		worker.SPAMWorkerNew(ctx, apis, slog, &cfg),
	}

	for _, w := range workers {
		if err := w.Config(ctx); err != nil {
			log.Panic(err)
		}
	}

	for _, w := range workers {
		if err := w.Spawn(ctx); err != nil {
			log.Panic(err)
		}
	}

	<-ctx.Done()
	time.Sleep(time.Second)

	log.Error("Bye!")

}
