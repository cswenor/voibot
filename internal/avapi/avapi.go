package avapi

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/algonode/voibot/internal/config"
	"github.com/sirupsen/logrus"
)

type AVAPI struct {
	cfg    *config.AVAPIConfig
	log    *logrus.Logger
	client *http.Client
}

func Make(ctx context.Context, acfg *config.AVAPIConfig, log *logrus.Logger) (*AVAPI, error) {

	// Create REST client

	c := &http.Client{
		Transport: &http.Transport{
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
		},
	}

	return &AVAPI{
		cfg:    acfg,
		log:    log,
		client: c,
	}, nil

}

func get[T any](ctx context.Context, c *http.Client, url string) (T, error) {
	var m T
	r, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return m, err
	}
	res, err := c.Do(r)
	if err != nil {
		return m, err
	}
	body, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil {
		return m, err
	}
	return parseJSON[T](body)
}

func parseJSON[T any](s []byte) (T, error) {
	var r T
	if err := json.Unmarshal(s, &r); err != nil {
		return r, err
	}
	return r, nil
}

func (g *AVAPI) makeURI(ctx context.Context, endp string) (context.Context, *http.Client, string) {
	return ctx, g.client, g.cfg.Address + endp
}
