package ethconfig

import (
	"encoding/json"
	"math/big"
	"os"
	"time"

	"github.com/ledgerwatch/erigon-lib/common"
	"github.com/ledgerwatch/erigon/core/types"
)

// Merlin config
type Merlin struct {
	*ReplaceBlocks
	VerifyZkProofConfigs []*VerifyZkProofConfig
}

// need replace block that make the block hash consistet
type ReplaceBlocks struct {
	Headers map[uint64]*types.Header `json:"headers"`
}

// VerifyZkProofConfig verify zk proof config
type VerifyZkProofConfig struct {
	//ForkID this forkid of config
	ForkID uint64
	// Verifier Address of the L1 verifier contract
	Verifier common.Address
	// TrustedAggregator trusted Aggregator that used for verify proof
	TrustedAggregator common.Address
}

func (m *Merlin) FindMerlinHeaderConfig(blockNum *big.Int) *types.Header {
	if m != nil && m.ReplaceBlocks != nil {
		if h, ok := m.Headers[blockNum.Uint64()]; ok {
			return h
		}
	}
	return nil
}
func ReadReplaceBlock(path string) (*ReplaceBlocks, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	m := ReplaceBlocks{}
	err = json.Unmarshal(data, &m)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

type GasPriceConf struct {
	Enable                     bool
	DefaultGasPrice            uint64
	MaxGasPrice                uint64
	IgnorePrice                uint64
	CheckBlocks                int
	Percentile                 int
	EnableGasPriceDynamicDecay bool
	GasPriceDynamicDecayFactor float64
	GlobalPending              int
	GlobalPendingDynamicFactor float64
	PendingGasLimit            uint64
	UpdatePeriod               time.Duration
}

var DefaultGPC = GasPriceConf{
	Enable:                     false,
	CheckBlocks:                5,
	Percentile:                 70,
	EnableGasPriceDynamicDecay: false,
	GasPriceDynamicDecayFactor: 0.05,
	GlobalPending:              10_000,
	GlobalPendingDynamicFactor: 1,
	PendingGasLimit:            22_000_000,
	UpdatePeriod:               10 * time.Second,
}
