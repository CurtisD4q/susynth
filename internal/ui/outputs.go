package ui

import (
	"github.com/susynth/susynth/internal/engine"
	"github.com/susynth/susynth/internal/osc"
)

type engineOutput = engine.Output

func openOSC(addr string) (engine.Output, error) {
	if err := validAddr(addr); err != nil {
		return nil, err
	}
	o, err := osc.Open(addr)
	if err != nil {
		return nil, err
	}
	return o, nil
}
