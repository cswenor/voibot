package worker

import (
	"context"
	"fmt"
	"time"
	"github.com/algonode/voibot/internal/config"
	"github.com/algorand/go-algorand-sdk/v2/crypto"
	"github.com/algorand/go-algorand-sdk/v2/mnemonic"
	"github.com/algorand/go-algorand-sdk/v2/transaction"
	"github.com/algorand/go-algorand-sdk/v2/types"
	"github.com/sirupsen/logrus"
	"go.uber.org/ratelimit"
)

const (
	SingletonMINE = "mine"
)

type Stx []byte

type MINEWorker struct {
	mineAccount crypto.Account
	txChan      chan Stx
	priceChan	chan float64 
	sParams     SParams
	WorkerCommon
}

func MINEWorkerNew(ctx context.Context, apis *WorkerAPIs, log *logrus.Logger, cfg *config.BotConfig) Worker {
	return &MINEWorker{
		WorkerCommon: WorkerCommon{
			cfg:        cfg,
			syncWorker: false,
			apis:       apis,
			log:        log.WithFields(logrus.Fields{"wrk": SingletonMINE}),
		},
	}
}

func (w *MINEWorker) setupMiner(ctx context.Context) error {
	mn, ok := w.cfg.PKeys["MINE"]
	if !ok {
		return fmt.Errorf("MINE mnemonic not found in conifg")
	}
	pk, err := mnemonic.ToPrivateKey(mn)
	if err != nil {
		return err
	}

	w.mineAccount, err = crypto.AccountFromPrivateKey(pk)
	if err != nil {
		return err
	}

	//Implement any other setup and config that the Miner worker needs so that the threads can utilize them
	

	return nil

}

func (w *MINEWorker) Config(ctx context.Context) error {
	if v, ok := w.cfg.WSnglt[SingletonMINE]; !ok || !v {
		w.log.Infof("%s disabled, skipping configuration", SingletonMINE)
		return nil
	}

	err := w.setupMiner(ctx)
	if err != nil {
		w.log.WithError(err).Panic("Error setting up miner")
		return nil
	}

	w.log.Infof("Miner %s booted with %d thread and rate %d", w.mineAccount.Address.String()[0:8], w.cfg.MINE.Threads, w.cfg.MINE.Rate)

	w.txChan = make(chan Stx, 500)

	return nil
}

func (w *MINEWorker) updateSuggestedParams(ctx context.Context) {
	txParams, err := w.apis.Aapi.Client.SuggestedParams().Do(ctx)
	if err != nil {
		w.log.WithError(err).Error("Error getting suggested tx params")
		return
	}
	w.log.Infof("Suggested first round is %d, minfee: %d", txParams.FirstRoundValid, txParams.MinFee)
	txParams.Fee = 1_000
	txParams.FlatFee = true
	w.sParams.Lock()
	w.sParams.params = &txParams
	w.sParams.Unlock()
}

func (w *MINEWorker) execSync(ctx context.Context, stx Stx) {
	sendResponse, err := w.apis.Aapi.Client.SendRawTransaction(stx).Do(ctx)
	if err != nil {
		w.log.WithError(err).Error("Error sending transaction")
		return
	}
	if sendResponse[0] == 'A' {
		w.log.Infof("Submitted transaction %s\n", sendResponse)
	}
}

func (w *MINEWorker) mineGen(ctx context.Context) {
	rl := ratelimit.New(w.cfg.MINE.Rate, ratelimit.WithoutSlack) // per second
	for {
		if ctx.Err() != nil {
			return
		}

		// var atc = transaction.AtomicTransactionComposer{}
		// signer := transaction.BasicAccountTransactionSigner{Account: w.mineAccount}
		// for n := 1; n <= 15; n++ {
		// 	if tx, err := w.makeTX(ctx); err == nil {
		// 		atc.AddTransaction(transaction.TransactionWithSigner{Txn: *tx, Signer: signer})
		// 	}
		// }

		// stxs, err := atc.GatherSignatures()
		// if err != nil {
		// 	continue
		// }

		// var serializedStxs []byte
		// for _, stx := range stxs {
		// 	serializedStxs = append(serializedStxs, stx...)
		// }
		// w.txChan <- serializedStxs

		rl.Take()

		//TODO - Only generate transaction to be submitted to the executing threads if price is not above limit
		if stx, err := w.makeSTX(ctx); err == nil {
			w.txChan <- stx
		}
	}
}

//TODO: Update to make app call transaction
func (w *MINEWorker) makeSTX(ctx context.Context) (Stx, error) {
	var params *types.SuggestedParams
	w.sParams.RLock()
	params = w.sParams.params
	w.sParams.RUnlock()

	txn, err := transaction.MakePaymentTxn(
		w.mineAccount.Address.String(),
		crypto.GenerateAccount().Address.String(),
		0,
		nil,
		"",
		*params)
	if err != nil {
		w.log.WithError(err).Error("Error creating transaction")
		return nil, err
	}

	_, signedTxn, err := crypto.SignTransaction(w.mineAccount.PrivateKey, txn)
	if err != nil {
		w.log.WithError(err).Error("Error signing transaction")
		return nil, err
	}
	return signedTxn, nil
}

func (w *MINEWorker) makeTX(ctx context.Context) (*types.Transaction, error) {
	var params *types.SuggestedParams
	w.sParams.RLock()
	params = w.sParams.params
	w.sParams.RUnlock()

	// buf := make([]byte, 1020)
	// rand.Read(buf)

	txn, err := transaction.MakePaymentTxn(
		w.mineAccount.Address.String(),
		crypto.GenerateAccount().Address.String(),
		0,
		nil,
		"",
		*params)
	if err != nil {
		w.log.WithError(err).Error("Error creating transaction")
		return nil, err
	}

	return &txn, nil
}

// Mining threads - Needs to mine while taking into account the shared price via the polling thread
func (w *MINEWorker) mineExec(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			close(w.txChan)
			return
		case stx, ok := <-w.txChan:
			if !ok {
				close(w.txChan)
				return
			}
			w.execSync(ctx, stx)
		}
	}
}

func (w *MINEWorker) paramsUpdater(ctx context.Context) {
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

func (w *MINEWorker) Spawn(ctx context.Context) error {
	if v, ok := w.cfg.WSnglt[SingletonMINE]; !ok || !v {
		w.log.Infof("%s disabled, not spawning", SingletonMINE)
		return nil
	}
	w.updateSuggestedParams(ctx)

	//Submit threads
	go w.paramsUpdater(ctx)
	go w.pricePollExec(ctx) // Polls for price to be used to determine whether or not to generate transactions to be submitted - Single Thread
	for i := 0; i < w.cfg.MINE.Threads; i++ {
		go w.mineExec(ctx) // Executing threads submitting transactions
	}
	go w.mineGen(ctx) // Generates transaction to be submitted - Single thread
	return nil
}

//Price polling thread - Needs to poll the price of the ORA token and share the price with the minimg threads via a channel
func (w *MINEWorker) pricePollExec(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			close(w.priceChan)
			return
		default:
			w.execPoll(ctx, stx)
		}
	}
}


func (w *MINEWorker) execPoll(ctx context.Context, stx Stx) {//Poll and get back price to be submitted to channel for mineing threads to read
	w.priceChan <- price
}
