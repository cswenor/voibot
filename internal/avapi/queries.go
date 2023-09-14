package avapi

import (
	"context"
)

type AddrUVoi map[string]uint64

type BallastInfo struct {
	Round   uint64   `json:"round"`
	Bparts  AddrUVoi `json:"bparts"`
	Bots    AddrUVoi `json:"bots"`
	Buffer  uint64   `json:"buffer"`
	Online  uint64   `json:"online"`
	Ballast uint64   `json:"ballast"`
}

func (g *AVAPI) GetBallastInfo(ctx context.Context) (*BallastInfo, error) {
	bi, err := get[BallastInfo](g.makeURI(ctx, "/v0/consensus/ballast"))
	if err != nil {
		g.log.WithError(err).Error("Error executing GetBallastInfo query")
		return nil, err
	}
	//g.log.Infof("BI:%v", bi)
	return &bi, nil
}
