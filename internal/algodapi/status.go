package algodapi

import (
	"context"
	"strings"
	"time"

	"github.com/algonode/voibot/internal/utils"
	"github.com/algorand/go-algorand-sdk/v2/client/v2/common/models"
)

func (a *AlgodAPI) Status(ctx context.Context) (*models.NodeStatus, error) {
	var status *models.NodeStatus

	getStatus := func(actx context.Context) (bool, error) {
		s, err := a.Client.Status().Do(actx)
		if err != nil {
			a.log.WithError(err).Warnf("Error fetching Status")
			if strings.Contains(err.Error(), "HTTP 404") ||
				strings.Contains(err.Error(), "HTTP 401") ||
				strings.Contains(err.Error(), "HTTP 400") {
				return true, err
			}
			return false, err
		}
		status = &s
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
	return status, nil
}

func (a *AlgodAPI) WaitForRoundAfter(ctx context.Context, round uint64) (*models.NodeStatus, error) {
	var status *models.NodeStatus

	getStatus := func(actx context.Context) (bool, error) {
		s, err := a.Client.StatusAfterBlock(round).Do(actx)
		if err != nil {
			a.log.WithError(err).Warnf("Error fetching Status")
			if strings.Contains(err.Error(), "HTTP 404") ||
				strings.Contains(err.Error(), "HTTP 401") ||
				strings.Contains(err.Error(), "HTTP 400") {
				return true, err
			}
			return false, err
		}
		status = &s
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
	return status, nil
}
