//go:build notzkevm
// +build notzkevm

package jsonrpc

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/ledgerwatch/erigon-lib/common/hexutil"

	"github.com/holiman/uint256"
	libcommon "github.com/ledgerwatch/erigon-lib/common"
	"github.com/ledgerwatch/erigon-lib/gointerfaces/txpool"
	txPoolProto "github.com/ledgerwatch/erigon-lib/gointerfaces/txpool"
	"github.com/ledgerwatch/erigon-lib/kv/kvcache"
	"github.com/stretchr/testify/require"

	"github.com/ledgerwatch/erigon/cmd/rpcdaemon/rpcdaemontest"
	"github.com/ledgerwatch/erigon/core"
	"github.com/ledgerwatch/erigon/core/types"
	"github.com/ledgerwatch/erigon/params"
	"github.com/ledgerwatch/erigon/rpc/rpccfg"
	"github.com/ledgerwatch/erigon/turbo/rpchelper"
	"github.com/ledgerwatch/erigon/turbo/stages/mock"
)

func TestTxPoolContent(t *testing.T) {
	m, require := mock.MockWithTxPool(t), require.New(t)
	chain, err := core.GenerateChain(m.ChainConfig, m.Genesis, m.Engine, m.DB, 1, func(i int, b *core.BlockGen) {
		b.SetCoinbase(libcommon.Address{1})
	})
	require.NoError(err)
	err = m.InsertChain(chain)
	require.NoError(err)

	ctx, conn := rpcdaemontest.CreateTestGrpcConn(t, m)
	txPool := txpool.NewTxpoolClient(conn)
	ff := rpchelper.New(ctx, nil, txPool, txpool.NewMiningClient(conn), func() {}, m.Log)
	agg := m.HistoryV3Components()
	api := NewTxPoolAPI(NewBaseApi(ff, kvcache.New(kvcache.DefaultCoherentConfig), m.BlockReader, agg, false, rpccfg.DefaultEvmCallTimeout, m.Engine, m.Dirs), m.DB, txPool, "")

	expectValue := uint64(1234)
	txn, err := types.SignTx(types.NewTransaction(0, libcommon.Address{1}, uint256.NewInt(expectValue), params.TxGas, uint256.NewInt(10*params.GWei), nil), *types.LatestSignerForChainID(m.ChainConfig.ChainID), m.Key)
	require.NoError(err)

	buf := bytes.NewBuffer(nil)
	err = txn.MarshalBinary(buf)
	require.NoError(err)

	reply, err := txPool.Add(ctx, &txpool.AddRequest{RlpTxs: [][]byte{buf.Bytes()}})
	require.NoError(err)
	for _, res := range reply.Imported {
		require.Equal(res, txPoolProto.ImportResult_SUCCESS, fmt.Sprintf("%s", reply.Errors))
	}

	content, err := api.Content(ctx)
	content_map := content.(map[string]map[string]map[string]*RPCTransaction)
	require.NoError(err)

	sender := m.Address.String()
	require.Equal(1, len(content_map["pending"][sender]))
	require.Equal(expectValue, content_map["pending"][sender]["0"].Value.ToInt().Uint64())

	status, err := api.Status(ctx)
	status_map := status.(map[string]hexutil.Uint)
	require.NoError(err)
	require.Len(status_map, 3)
	require.Equal(status_map["pending"], hexutil.Uint(1))
	require.Equal(status_map["queued"], hexutil.Uint(0))
}
