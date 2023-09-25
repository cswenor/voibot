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

package algodapi

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/algonode/voibot/internal/config"
	"github.com/algorand/go-algorand-sdk/v2/client/v2/algod"
	"github.com/sirupsen/logrus"
)

type AlgodAPI struct {
	cfg    *config.NodeConfig
	log    *logrus.Logger
	Client *algod.Client
}

func Make(ctx context.Context, acfg *config.NodeConfig, log *logrus.Logger) (*AlgodAPI, error) {

	// Create an algod client
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 15 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 5 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		DisableKeepAlives:     false,
		MaxIdleConnsPerHost:   100,
		MaxIdleConns:          100,
	}

	algodClient, err := algod.MakeClientWithTransport(acfg.Address, acfg.Token, nil, transport)
	if err != nil {
		return nil, fmt.Errorf("failed to make algod client: %s\n", err)
	}

	return &AlgodAPI{
		cfg:    acfg,
		log:    log,
		Client: algodClient,
	}, nil

}
