package ethconfig

import (
	"encoding/json"
	"math/big"
	"os"

	"github.com/ledgerwatch/erigon/core/types"
)

type Merlin struct {
	Headers map[uint64]*types.Header `json:"headers"`
}

func (m *Merlin) FindMerlinHeaderConfig(blockNum *big.Int) *types.Header {
	if m != nil {
		if h, ok := m.Headers[blockNum.Uint64()]; ok {
			return h
		}
	}
	return nil
}
func ReadMerlinCfg(path string) (*Merlin, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	m := Merlin{}
	err = json.Unmarshal(data, &m)
	if err != nil {
		return nil, err
	}
	return &m, nil
}
