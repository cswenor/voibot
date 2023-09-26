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
	SingletonSPAM = "spam"
)

type Stx []byte

type SPAMWorker struct {
	spamAccount crypto.Account
	txChan      chan Stx
	sParams     SParams
	WorkerCommon
}

func SPAMWorkerNew(ctx context.Context, apis *WorkerAPIs, log *logrus.Logger, cfg *config.BotConfig) Worker {
	return &SPAMWorker{
		WorkerCommon: WorkerCommon{
			cfg:        cfg,
			syncWorker: false,
			apis:       apis,
			log:        log.WithFields(logrus.Fields{"wrk": SingletonSPAM}),
		},
	}
}

func (w *SPAMWorker) setupSpammer(ctx context.Context) error {
	mn, ok := w.cfg.PKeys["SPAM"]
	if !ok {
		return fmt.Errorf("SPAM mnemonic not found in conifg")
	}
	pk, err := mnemonic.ToPrivateKey(mn)
	if err != nil {
		return err
	}

	w.spamAccount, err = crypto.AccountFromPrivateKey(pk)
	if err != nil {
		return err
	}

	return nil

}

func (w *SPAMWorker) Config(ctx context.Context) error {
	if v, ok := w.cfg.WSnglt[SingletonSPAM]; !ok || !v {
		w.log.Infof("%s disabled, skipping configuration", SingletonSPAM)
		return nil
	}

	err := w.setupSpammer(ctx)
	if err != nil {
		w.log.WithError(err).Panic("Error setting up ballast")
		return nil
	}

	w.log.Infof("Spammer %s booted with %d thread", w.spamAccount.Address.String()[0:8], w.cfg.SPAM.Threads)

	w.txChan = make(chan Stx, 500)

	return nil
}

func (w *SPAMWorker) updateSuggestedParams(ctx context.Context) {
	txParams, err := w.apis.Aapi.Client.SuggestedParams().Do(ctx)
	if err != nil {
		w.log.WithError(err).Error("Error getting suggested tx params")
		return
	}
	w.log.Infof("Suggested first round is %d, minfee: %d", txParams.FirstRoundValid, txParams.MinFee)
	txParams.Fee = 2_000
	txParams.FlatFee = true
	w.sParams.Lock()
	w.sParams.params = &txParams
	w.sParams.Unlock()
}

func (w *SPAMWorker) execSync(ctx context.Context, stx Stx) {
	sendResponse, err := w.apis.Aapi.Client.SendRawTransaction(stx).Do(ctx)
	if err != nil {
		w.log.WithError(err).Error("Error sending transaction")
		return
	}
	w.log.Infof("Submitted transaction %s\n", sendResponse)
}

func (w *SPAMWorker) spamGen(ctx context.Context) {
	rl := ratelimit.New(500) // per second
	for {
		if ctx.Err() != nil {
			return
		}

		// var atc = transaction.AtomicTransactionComposer{}
		// signer := transaction.BasicAccountTransactionSigner{Account: w.spamAccount}
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
		if stx, err := w.makeSTX(ctx); err == nil {
			w.txChan <- stx
		}
	}
}

func (w *SPAMWorker) makeSTX(ctx context.Context) (Stx, error) {
	var params *types.SuggestedParams
	w.sParams.RLock()
	params = w.sParams.params
	w.sParams.RUnlock()

	txn, err := transaction.MakePaymentTxn(
		w.spamAccount.Address.String(),
		crypto.GenerateAccount().Address.String(),
		0,
		nil,
		"",
		*params)
	if err != nil {
		w.log.WithError(err).Error("Error creating transaction")
		return nil, err
	}

	_, signedTxn, err := crypto.SignTransaction(w.spamAccount.PrivateKey, txn)
	if err != nil {
		w.log.WithError(err).Error("Error signing transaction")
		return nil, err
	}
	return signedTxn, nil
}

func (w *SPAMWorker) makeTX(ctx context.Context) (*types.Transaction, error) {
	var params *types.SuggestedParams
	w.sParams.RLock()
	params = w.sParams.params
	w.sParams.RUnlock()

	// buf := make([]byte, 1020)
	// rand.Read(buf)

	txn, err := transaction.MakePaymentTxn(
		w.spamAccount.Address.String(),
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

func (w *SPAMWorker) spamExec(ctx context.Context) {
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

func (w *SPAMWorker) paramsUpdater(ctx context.Context) {
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

func (w *SPAMWorker) Spawn(ctx context.Context) error {
	if v, ok := w.cfg.WSnglt[SingletonSPAM]; !ok || !v {
		w.log.Infof("%s disabled, not spawning", SingletonSPAM)
		return nil
	}
	w.updateSuggestedParams(ctx)
	go w.paramsUpdater(ctx)
	for i := 0; i < w.cfg.SPAM.Threads; i++ {
		go w.spamExec(ctx)
	}
	go w.spamGen(ctx)
	return nil
}
