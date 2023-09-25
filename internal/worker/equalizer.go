package worker

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/algonode/voibot/internal/avapi"
	"github.com/algonode/voibot/internal/config"
	"github.com/algorand/go-algorand-sdk/v2/crypto"
	"github.com/algorand/go-algorand-sdk/v2/mnemonic"
	"github.com/algorand/go-algorand-sdk/v2/transaction"
	"github.com/algorand/go-algorand-sdk/v2/types"
	"github.com/sirupsen/logrus"
)

const (
	SingletonEQUALIZER = "equalizer"
)

type RoundStatus struct {
	round uint64
}

type BallastAccounts struct {
	bmap    map[string]uint64
	myTotal uint64
}

type SParams struct {
	sync.RWMutex
	params *types.SuggestedParams
}

type EQUALIZERWorker struct {
	lastRound      atomic.Uint64
	ballastAccount crypto.Account
	ballast        uint64
	bAccounts      int
	myAccounts     BallastAccounts
	rsChan         chan *RoundStatus
	sParams        SParams
	WorkerCommon
}

func EQUALIZERWorkerNew(ctx context.Context, apis *WorkerAPIs, log *logrus.Logger, cfg *config.BotConfig) Worker {
	return &EQUALIZERWorker{
		myAccounts: BallastAccounts{myTotal: 0, bmap: make(map[string]uint64)},
		WorkerCommon: WorkerCommon{
			cfg:        cfg,
			syncWorker: false,
			apis:       apis,
			log:        log.WithFields(logrus.Fields{"wrk": SingletonEQUALIZER}),
		},
	}
}

func (w *EQUALIZERWorker) setupBallast(ctx context.Context) error {
	bi, err := w.apis.AVapi.GetBallastInfo(ctx)
	if err != nil {
		return err
	}
	mn, ok := w.cfg.PKeys["EQBOT"]
	if !ok {
		return fmt.Errorf("EQBOT mnemonic not found in conifg")
	}
	pk, err := mnemonic.ToPrivateKey(mn)
	if err != nil {
		return err
	}

	w.ballastAccount, err = crypto.AccountFromPrivateKey(pk)
	if err != nil {
		return err
	}

	bAddr := w.ballastAccount.Address.String()

	for addr := range bi.Bparts {
		ai, err := w.apis.Aapi.GetAccountInfo(ctx, addr)
		if err != nil {
			return err
		}

		if ai.AuthAddr == bAddr {
			w.log.Infof("Adding %s as managed by %s bot with %d Voi deployed", ai.Address[0:8], w.ballastAccount.Address.String()[0:8], ai.Amount/1_000_000)
			w.myAccounts.bmap[ai.Address] = 0
		}

	}
	w.update(bi)
	return nil

}

func (w *EQUALIZERWorker) Config(ctx context.Context) error {
	if v, ok := w.cfg.WSnglt[SingletonEQUALIZER]; !ok || !v {
		w.log.Infof("%s disabled, skipping configuration", SingletonEQUALIZER)
		return nil
	}

	err := w.setupBallast(ctx)
	if err != nil {
		w.log.WithError(err).Panic("Error setting up ballast")
		return nil
	}

	w.log.Infof("Equalizer %s booted with %d accounts, %d Voi ballast deployed and %d Voi in treasury", w.ballastAccount.Address.String()[0:8], len(w.myAccounts.bmap), w.myAccounts.myTotal/1_000_000, w.ballast/1_000_000)

	w.rsChan = make(chan *RoundStatus, 1)

	return nil
}

func (w *EQUALIZERWorker) blockFollower(ctx context.Context) {
	//Loop until Algoverse gets cancelled
	var lastRound uint64 = 0
	for {
		if ctx.Err() != nil {
			return
		}
		s, err := w.apis.Aapi.WaitForRoundAfter(ctx, lastRound)
		if err != nil {
			w.log.WithError(err).Error("Error waiting for the next round")
			continue
		}
		w.log.Infof("Last round is %d", s.LastRound)
		if lastRound > 0 {
			w.rsChan <- &RoundStatus{round: s.LastRound}
		}
		lastRound = s.LastRound
		w.lastRound.Store(lastRound)
	}
}

func (w *EQUALIZERWorker) update(bi *avapi.BallastInfo) {
	var total uint64
	total = 0
	for addr := range w.myAccounts.bmap {
		if v, ok := bi.Bparts[addr]; ok {
			total += v
			w.myAccounts.bmap[addr] = v
		} else {
			w.log.Warnf("No update for ballast account %s", addr)
		}
	}
	w.myAccounts.myTotal = total
	w.bAccounts = len(bi.Bparts)
	if v, ok := bi.Bots[w.ballastAccount.Address.String()]; ok {
		w.ballast = v
	} else {
		w.log.Errorf("No treasury info for %s", w.ballastAccount.Address.String())
	}
}

func (w *EQUALIZERWorker) calculateBalastUpdate(bi *avapi.BallastInfo) (target uint64, increase uint64, decrease uint64) {
	target = (bi.Buffer + bi.Online) * uint64(w.cfg.EQCFG.Target) / (100 - uint64(w.cfg.EQCFG.Target))
	increase = 0
	decrease = 0
	deployed := w.myAccounts.myTotal
	if target > deployed {
		increase = target - deployed
		if increase < 1_000_000 {
			increase = 0
		}
		return
	}
	decrease = deployed - target
	diffPct := (1000 * decrease / deployed)
	if diffPct < 10 {
		decrease = 0
		return
	}
	return
}

func (w *EQUALIZERWorker) updateSuggestedParams(ctx context.Context) {
	txParams, err := w.apis.Aapi.Client.SuggestedParams().Do(ctx)
	if err != nil {
		w.log.WithError(err).Error("Error getting suggested tx params")
		return
	}
	w.log.Infof("Suggested first round is %d", txParams.FirstRoundValid)
	w.sParams.Lock()
	w.sParams.params = &txParams
	w.sParams.Unlock()
}

func (w *EQUALIZERWorker) ensureBallast(ctx context.Context, tVoiPerAccount uint64) {
	var params *types.SuggestedParams
	w.sParams.Lock()
	params = w.sParams.params
	w.sParams.Unlock()
	params.FirstRoundValid = types.Round(w.lastRound.Load())
	params.LastRoundValid = params.FirstRoundValid + 5
	w.log.Infof("New target per ballast account %d", tVoiPerAccount/1_000_000)
	for baAddr, baVoi := range w.myAccounts.bmap {
		if tVoiPerAccount > baVoi {
			diff := tVoiPerAccount - baVoi
			if diff > w.ballast {
				diff = w.ballast - 1_000_000
				w.log.Errorf("Insufficient treasury in %s", w.ballastAccount.Address)
			}
			w.log.Infof("Adding %d to %s from %s; %d .. %d", diff/1_000_000, baAddr[0:8], w.ballastAccount.Address.String()[0:8], baVoi/1_000_000, tVoiPerAccount/1_000_000)

			txn, err := transaction.MakePaymentTxn(
				w.ballastAccount.Address.String(),
				baAddr,
				diff,
				nil,
				"",
				*params)
			if err != nil {
				w.log.WithError(err).Error("Error creating transaction")
				return
			}

			_, signedTxn, err := crypto.SignTransaction(w.ballastAccount.PrivateKey, txn)
			if err != nil {
				w.log.WithError(err).Error("Error signing transaction")
				return
			}
			sendResponse, err := w.apis.Aapi.Client.SendRawTransaction(signedTxn).Do(ctx)
			if err != nil {
				w.log.WithError(err).Error("Error sending transaction")
				return
			}
			w.log.Infof("Submitted transaction %s\n", sendResponse)
		}
		if tVoiPerAccount < baVoi {
			diff := baVoi - tVoiPerAccount
			w.log.Infof("Removing %d from %s to %s; %d .. %d", diff/1_000_000, baAddr[0:8], w.ballastAccount.Address.String()[0:8], baVoi/1_000_000, tVoiPerAccount/1_000_000)

			txn, err := transaction.MakePaymentTxn(
				baAddr,
				w.ballastAccount.Address.String(),
				diff,
				nil,
				"",
				*params)
			if err != nil {
				w.log.WithError(err).Error("Error creating transaction")
				return
			}

			_, signedTxn, err := crypto.SignTransaction(w.ballastAccount.PrivateKey, txn)
			if err != nil {
				w.log.WithError(err).Error("Error signing transaction")
				return
			}
			sendResponse, err := w.apis.Aapi.Client.SendRawTransaction(signedTxn).Do(ctx)
			if err != nil {
				w.log.WithError(err).Error("Error sending transaction")
				return
			}
			w.log.Infof("Submitted transaction %s\n", sendResponse)
		}

	}
	time.Sleep(time.Second * time.Duration(w.cfg.EQCFG.Interval))
}

func (w *EQUALIZERWorker) addBallast(ctx context.Context, increase uint64) {
	inc := uint64(float64(increase) * w.cfg.EQCFG.Upfactor)
	//	w.log.Infof("INC: %d .. %d", increase, inc)
	//	tVoiPerAccount := (w.myAccounts.myTotal + inc) / uint64(len(w.myAccounts.bmap))
	tVoiPerAccount := (w.myAccounts.myTotal + inc) / uint64(w.bAccounts)
	w.ensureBallast(ctx, tVoiPerAccount)
}

func (w *EQUALIZERWorker) removeBallast(ctx context.Context, decrease uint64) {
	dec := uint64(float64(decrease) * w.cfg.EQCFG.Downfactor)
	//	w.log.Infof("DEC: %d .. %d", decrease, dec)
	tVoiPerAccount := (w.myAccounts.myTotal - dec) / uint64(len(w.myAccounts.bmap))
	w.ensureBallast(ctx, tVoiPerAccount)
}

func (w *EQUALIZERWorker) equalize(ctx context.Context, rs uint64) {
	w.log.Infof("Equalizing at round %d", rs)
	if bi, err := w.apis.AVapi.GetBallastInfo(ctx); err == nil {
		lag := int64(rs) - int64(bi.Round)
		if rs > 0 && lag > 1 {
			w.log.Errorf("Indexer behind by %d rounds", lag)
			return
		}
		w.update(bi)

		target, inc, dec := w.calculateBalastUpdate(bi)
		if inc+dec > 0 {
			w.log.Infof("Target:%d, increase:%d, decrease:%d", target/1_000_000, inc/1_000_000, dec/1_000_000)
			if inc > 0 {
				w.addBallast(ctx, inc)
			}
			if dec > 0 {
				w.removeBallast(ctx, dec)
			}
		} else {
			w.log.Infof("No ballast update for round %d", bi.Round)
		}
	}
}

func (w *EQUALIZERWorker) equalizerLoop(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			close(w.rsChan)
			return
		case rs, ok := <-w.rsChan:
			if !ok {
				close(w.rsChan)
				return
			}
			w.equalize(ctx, rs.round)
		}
	}
}

func (w *EQUALIZERWorker) paramsUpdater(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	for {
		if ctx.Err() != nil {
			ticker.Stop()
			return
		}
		select {
		case <-ctx.Done():
			ticker.Stop()
			return
		case <-ticker.C:
			w.updateSuggestedParams(ctx)
		}
	}
}

func (w *EQUALIZERWorker) Spawn(ctx context.Context) error {
	if v, ok := w.cfg.WSnglt[SingletonEQUALIZER]; !ok || !v {
		w.log.Infof("%s disabled, not spawning", SingletonEQUALIZER)
		return nil
	}
	w.updateSuggestedParams(ctx)
	go w.paramsUpdater(ctx)
	go w.blockFollower(ctx)
	go w.equalizerLoop(ctx)
	return nil
}
