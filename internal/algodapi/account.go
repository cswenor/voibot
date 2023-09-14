package algodapi

import (
	"context"
	"strings"
	"time"

	"github.com/algonode/voibot/internal/utils"
	"github.com/algorand/go-algorand-sdk/v2/client/v2/common/models"
)

func (a *AlgodAPI) GetAccountInfo(ctx context.Context, addr string) (*models.Account, error) {
	var account *models.Account
	account = nil

	getStatus := func(actx context.Context) (bool, error) {
		s, err := a.Client.AccountInformation(addr).Do(actx)
		if err != nil {
			a.log.WithError(err).Warnf("Error fetching block")
			if strings.Contains(err.Error(), "HTTP 404") ||
				strings.Contains(err.Error(), "HTTP 401") ||
				strings.Contains(err.Error(), "HTTP 400") {
				return true, err
			}
			return false, err
		}
		account = &s
		return false, nil
	}

	if err := utils.Backoff(
		ctx,
		getStatus,
		time.Second*10,
		time.Millisecond*200,
		time.Second*10, 0); err != nil {
		return nil, err
	}
	return account, nil
}
