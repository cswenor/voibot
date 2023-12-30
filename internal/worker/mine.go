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
	"github.com/algorand/go-algorand-sdk/v2/client/v2/algod"
	"github.com/algorand/go-algorand-sdk/v2/client/v2/common"
	"github.com/sirupsen/logrus"
	"go.uber.org/ratelimit"
)

const (
	SingletonMINE = "mine"
)

type Stx []byte

type MINEWorker struct {
	mineAccount 	crypto.Account
	txChan      	chan Stx
	price			FloatValueContainer 
	rank			IntValueContainer 
	sParams     	SParams
	depositAddress 	types.Address
	contract 		abi.Contract
	method 			abi.Method
	appID			uint64
	WorkerCommon
}

type FloatValueContainer struct {
	value		float64 
	mu			sync.RWMutex
}

type IntValueContainer struct {
	value		uint
	mu			sync.RWMutex
}

type AppData struct {
	ID                 uint64
	Asset              uint64
	Block              uint64
	TotalEffort        uint64
	TotalTransactions  uint64
	Halving            uint64
	HalvingSupply      uint64
	MinedSupply        uint64
	MinerReward        uint64
	LastMiner          string
	LastMinerEffort    uint64
	CurrentMiner       string
	CurrentMinerEffort uint64
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
	da, ok := w.cfg.MINE.DepositAddress
	if !ok {
		return fmt.Errorf("MINE Deposit Address not found in conifg")
	}

	w.depositAddress, err = types.DecodeAddress(da)
	if err != nil {
		return fmt.Errorf("MINE failed to decode deposit address")
	}

	abiPath, ok := w.cfg.MINE.Abi
	if !ok {
		return fmt.Errorf("MINE ABI not found in conifg")
	}

	b, err := os.ReadFile(abiPath)
	if err != nil {
		return fmt.Errorf("failed to read abi.json file", "err", err)
	}

	err = json.Unmarshal(b, &w.contract)
	if err != nil {
		return fmt.Errorf("failed to unmarshal abi.json to abi contract", "err", err)
	}

	id, ok := w.cfg.MINE.AppId
	if !ok {
		return fmt.Errorf("MINE AppId not found in conifg")
	}

	w.appID, err = strconv.ParseUint(id, 10, 64)
	if err != nil {
		return fmt.Errorf("failed to parse app id to uint64", "err", err, "appID", id)
	}
	

	w.method, err = w.contract.GetMethodByName("mine")
	if err != nil {
		return fmt.Errorf("failed to get method from contract", "methodName", "mine")
	}
	
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

	w.checkDepositOptedIn(ctx)
	w.checkMiner(ctx)

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

	// reponse, err := composer.Execute(w.apis.Aapi.Client, ctx, 5)
	// if err != nil {
	// 	w.log.WithError(err).Error("Error sending transaction")
	// 	return
	// }

	// w.log.Infof("Submitted transaction %s\n", reponse)

}

func (w *MINEWorker) createGroup(ctx context.Context, appData AppData, start uint64, end uint64) error {
	var params *types.SuggestedParams
	w.sParams.RLock()
	params = w.sParams.params
	w.sParams.RUnlock()

	composer := transaction.AtomicTransactionComposer{}

	for i := start; i < end; i++ {
		composer.AddMethodCall(transaction.AddMethodCallParams{
			AppID:           w.appID,
			Method:          w.method,
			MethodArgs:      []any{w.depositAddress},
			Sender:          w.mineAccount.Address,
			SuggestedParams: params,
			Signer:          transaction.BasicAccountTransactionSigner{Account: w.mineAccount},
			ForeignAccounts: []string{appData.LastMiner, w.depositAddress.String()},
			ForeignAssets:   []uint64{appData.Asset},
			Note:            []byte(fmt.Sprint(i)),
		})
	}

	stxs, err := composer.GatherSignatures()
	if err != nil {
		continue
	}

	var serializedStxs []byte
	for _, stx := range stxs {
		serializedStxs = append(serializedStxs, stx...)
	}

	return serializedStxs;
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

		//TODO - Only generate transaction to be submitted to the executing threads if price is not above limit or our rank is number 1
		// The price limit is defined as the cost of the number of transactions (already submitted and not) required to be submitted being greater than that currentPrice of the asset.
		w.price.mu.Lock()
		currentPrice := w.price.value
		w.price.mu.Unlock()

		w.rank.mu.Lock()
		currentRank := w.rank.value
		w.rank.mu.Unlock()

		appData, err := w.getApplicationData(ctx)

		//TODO: Determine how many transactions to send to make rank number 1 if previous criteria met
		if stx, err := w.createGroup(ctx, appData, 0, 1); err == nil {
			w.txChan <- stx
		}
	}
}


//TODO: Update to make app call transaction
func (w *MINEWorker) makeSTX(ctx context.Context) (Stx, error) {
	txn, err := w.makeTX(ctx);

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
	go w.rankPollExec(ctx) // Polls for rank to be used to determine whether or not to generate transactions to be submitted - Single Thread
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
			return
		default:
			w.execPoll(ctx, stx)
		}
	}
}


func (w *MINEWorker) execPoll(ctx context.Context, stx Stx) {//TODO: Implement logic to get current price of asset
	currentPrice := 0.0
	w.price.mu.Lock()
	w.price.value = currentPrice
	w.price.mu.Unlock()
}

//Rank polling thread - Needs to poll the price of the ORA token and share the price with the minimg threads via a channel
func (w *MINEWorker) rankPollExec(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		default:
			w.execRank(ctx, stx)
		}
	}
}

func (w *MINEWorker) execRank(ctx context.Context, stx Stx) {//TODO: Implement logic to get current rank of our miner
	currentRank := 0
	w.rank.mu.Lock()
	w.rank.value = currentRank
	w.rank.mu.Unlock()
}



func (w *MINEWorker) checkDepositOptedIn(ctx context.Context) {
	depositInfo, err := w.apis.Aapi.Client.AccountInformation(w.depositAddress.String()).Do(ctx)
	if err != nil {
		return fmt.Errorf("failed to get deposit address info", "err", err)
	}
	appInfo, err := m.getApplicationData(ctx)
	if err != nil {
		return fmt.Errorf("failed to get application data", "err", err)
	}

	found := false

	for _, a := range depositInfo.AppsLocalState {
		if a.Id == appInfo.ID {
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("deposit address not opted in to app", "appId", appInfo.ID)
	}

	found = false

	for _, a := range depositInfo.Assets {
		if a.AssetId == appInfo.Asset {
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("deposit address not opted in to asset", "assetId", appInfo.Asset)
	}
}


func (w *MINEWorker) getApplicationData(ctx context.Context) (AppData, error) {
	app, err := w.apis.Aapi.Client.GetApplicationByID(w.appID).Do(ctx)
	if err != nil {
		return AppData{}, err
	}

	appData := AppData{
		ID: appID,
	}

	for _, gs := range app.Params.GlobalState {
		name, err := base64.StdEncoding.DecodeString(gs.Key)
		if err != nil {
			return AppData{}, err
		}

		switch string(name) {
		case "token":
			appData.Asset = gs.Value.Uint
		case "block":
			appData.Block = gs.Value.Uint
		case "total_effort":
			appData.TotalEffort = gs.Value.Uint
		case "total_transactions":
			appData.TotalTransactions = gs.Value.Uint
		case "halving":
			appData.Halving = gs.Value.Uint
		case "halving_supply":
			appData.HalvingSupply = gs.Value.Uint
		case "mined_supply":
			appData.MinedSupply = gs.Value.Uint
		case "miner_reward":
			appData.MinerReward = gs.Value.Uint
		case "last_miner":
			b, err := base64.StdEncoding.DecodeString(gs.Value.Bytes)
			if err != nil {
				return AppData{}, err
			}

			appData.LastMiner, err = types.EncodeAddress(b)
			if err != nil {
				return AppData{}, err
			}
		case "last_miner_effort":
			appData.LastMinerEffort = gs.Value.Uint
		case "current_miner":
			b, err := base64.StdEncoding.DecodeString(gs.Value.Bytes)
			if err != nil {
				return AppData{}, err
			}

			appData.CurrentMiner, err = types.EncodeAddress(b)
			if err != nil {
				return AppData{}, err
			}
		case "current_miner_effort":
			appData.CurrentMinerEffort = gs.Value.Uint
		}
	}

	slog.Info("appData", "appData", appData)

	return appData, nil
}


func (w *MINEWorker) checkMiner(ctx context.Context) {
	minerInfo, err := w.getBareAccount(ctx, w.mineAccount.Address)
	if err != nil {
		return fmt.Errorf("failed to get miner account info", "err", err)
	}

	minerBalance := minerInfo.Amount - minerInfo.MinBalance
	if minerBalance < 1000000 {
		return fmt.Errorf("miner has low balance, please fund before mining", "balance", float64(minerBalance)/math.Pow10(6))
	}
}

func (w *MINEWorker) getBareAccount(ctx context.Context, account types.Address) (AccountWithMinBalance, error) {
	var response AccountWithMinBalance
	var params = algod.AccountInformationParams{
		Exclude: "all",
	}

	err := (*common.Client)(w.apis.Aapi.Client).Get(ctx, &response, fmt.Sprintf("/v2/accounts/%s", account.String()), params, nil)
	if err != nil {
		return AccountWithMinBalance{}, err
	}
	return response, nil
}