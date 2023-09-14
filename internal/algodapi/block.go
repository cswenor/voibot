package algodapi

import (
	"context"
	"strings"
	"time"

	"github.com/algonode/voibot/internal/utils"
	"github.com/algorand/go-algorand-sdk/v2/client/v2/common/models"
	"github.com/algorand/go-algorand-sdk/v2/encoding/msgpack"
)

func (a *AlgodAPI) GetBlockRaw(ctx context.Context, round uint64) (*models.BlockResponse, error) {
	var block models.BlockResponse

	getStatus := func(actx context.Context) (bool, error) {
		s, err := a.Client.BlockRaw(round).Do(actx)
		if err != nil {
			a.log.WithError(err).Warnf("Error fetching block")
			if strings.Contains(err.Error(), "HTTP 404") ||
				strings.Contains(err.Error(), "HTTP 401") ||
				strings.Contains(err.Error(), "HTTP 400") {
				return true, err
			}
			return false, err
		}
		err = msgpack.Decode(s, &block)
		if err != nil {
			a.log.WithError(err).Warnf("Error decoding block")
			return false, err
		}

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
	return &block, nil
}
