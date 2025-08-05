package jsonrpc

import (
	"context"
	"fmt"
	"math/big"
	"testing"

	"encoding/json"

	"github.com/ledgerwatch/erigon-lib/chain"
	libcommon "github.com/ledgerwatch/erigon-lib/common"
	"github.com/ledgerwatch/erigon/eth/ethconfig"
	"github.com/ledgerwatch/erigon/zk/syncer"

	"github.com/iden3/go-iden3-crypto/keccak256"
	"github.com/ledgerwatch/erigon/accounts/abi"
	"github.com/ledgerwatch/erigon/common"
	"github.com/ledgerwatch/erigon/ethclient"
	types "github.com/ledgerwatch/erigon/zk/rpcdaemon"
	"github.com/ledgerwatch/erigon/zkevm/etherman"
	"github.com/ledgerwatch/erigon/zkevm/hex"
	"github.com/ledgerwatch/erigon/zkevm/jsonrpc/client"
	jtypes "github.com/ledgerwatch/erigon/zkevm/jsonrpc/types"
	"github.com/stretchr/testify/require"
)

type remoteT struct {
	conf       *ethconfig.Merlin
	rclientURL string
	ethClient  *ethclient.Client
	rollupABIs map[uint64]*abi.ABI
	blockHash  libcommon.Hash
	txindex    uint
	chainId    uint64
	forkID     uint64
}

func newRemoteTest(m *MerlinAPIImpl, rclient string, eclient *ethclient.Client, blockHash libcommon.Hash, txindex uint, chainId, forkID uint64) *remoteT {
	return &remoteT{
		conf:       m.cfg,
		rclientURL: rclient,
		ethClient:  eclient,
		rollupABIs: m.rollupABIs,
		blockHash:  blockHash,
		txindex:    txindex,
		chainId:    chainId,
		forkID:     forkID,
	}
}

func (r *remoteT) getSnarkParamFromRemote(ctx context.Context, param *verifyBatchesTrustedAggregatorParam, sender libcommon.Address) (interface{}, error) {
	oldBatch, err := BatchByNumber(ctx, r.rclientURL, big.NewInt(0).SetUint64(param.initNumBatch))
	if err != nil {
		return nil, fmt.Errorf("couldn't load verify batch from state by number %v, error %v", param.initNumBatch, err)
	}
	newBatch, err := BatchByNumber(ctx, r.rclientURL, big.NewInt(0).SetUint64(param.finalNewBatch))
	if err != nil {
		return nil, fmt.Errorf("couldn't load verify batch from state by number %v, error %v", param.initNumBatch, err)
	}
	return InputSnark{
		Sender:           sender,
		OldStateRoot:     oldBatch.StateRoot,
		OldAccInputHash:  oldBatch.AccInputHash,
		InitNumBatch:     param.initNumBatch,
		ChainId:          r.chainId,
		ForkID:           r.forkID,
		NewStateRoot:     newBatch.StateRoot,
		NewAccInputHash:  newBatch.AccInputHash,
		NewLocalExitRoot: newBatch.LocalExitRoot,
		FinalNewBatch:    param.finalNewBatch,
	}, nil
}

func (r *remoteT) getVerifyBatchesParam(blockHash libcommon.Hash, forkID uint64) (*verifyBatchesTrustedAggregatorParam, error) {
	tx, err := r.ethClient.TransactionInBlock(context.Background(), blockHash, r.txindex)
	if err != nil {
		return nil, err
	}
	if forkID < uint64(chain.ForkID8Elderberry) {
		return parseVerifyBatchesTrustedAggregatorOldInput(r.rollupABIs[uint64(chain.ForkID5Dragonfruit)], tx.GetData())
	}
	return parseVerifyBatchesTrustedAggregatorInput(r.rollupABIs[uint64(chain.ForkID8Elderberry)], tx.GetData())
}

func (r *remoteT) getVerifyBlockNumRange(startBatch, endBatch uint64) (interface{}, error) {
	return blockRange{
		start: 0,
		end:   0,
	}, nil
}

func BatchByNumber(ctx context.Context, url string, number *big.Int) (*types.Batch, error) {
	bn := jtypes.LatestBatchNumber
	if number != nil {
		bn = jtypes.BatchNumber(number.Int64())
	}
	response, err := client.JSONRPCCall(url, "zkevm_getBatchByNumber", bn, true)
	if err != nil {
		return nil, err
	}

	if response.Error != nil {
		return nil, fmt.Errorf(response.Error.Message)
	}

	var result *types.Batch
	err = json.Unmarshal(response.Result, &result)
	if err != nil {
		return nil, err
	}

	return result, nil
}

func TestVerifyTestnet(t *testing.T) {
	cfg := etherman.Config{
		URL:       "https://testnet-l1.merlinchain.io",
		L1ChainID: 55555,
		L2ChainID: 686868,
	}

	ethermanClient, err := ethclient.Dial(cfg.URL)
	require.NoError(t, err)

	conf := &ethconfig.Merlin{
		VerifyZkProofConfigs: []*ethconfig.VerifyZkProofConfig{{
			ForkID:            uint64(chain.ForkID8Elderberry),
			Verifier:          libcommon.HexToAddress("0xf81BC46a1277EF1e7BF0AC97C990d10131154458"), //
			TrustedAggregator: libcommon.HexToAddress("0x719647fcce805a0dae3a80c4a607c1792cff5d3c"), //
		}},
	}

	mpoints := NewMerlinAPI(nil, nil, conf, nil, newTestL1Syncer(ethermanClient))
	blockHash := libcommon.HexToHash("0xd538e2d6e98ffe176f60ea3401ffb54d0c0c3fbbf05bcef442cb7f6018fbe4e0")
	txindex := uint(0)

	ret := newRemoteTest(mpoints, "http://43.134.127.80:8123", ethermanClient, blockHash, txindex, cfg.L2ChainID, uint64(chain.ForkID8Elderberry))
	mpoints.setRemote(ret)

	zkp, snark, err := mpoints.getZkProofMeta(context.Background(), blockHash, uint64(chain.ForkID8Elderberry))
	require.NoError(t, err)

	zkpp := &ZKProof{
		ForkID:     zkp.forkID,
		Proof:      zkp.proof,
		PubSignals: zkp.pubSignals,
		RpubSignals: &RawPubSignals{
			Snark:  snark,
			Rfield: RFIELD,
		},
	}

	data, _ := json.MarshalIndent(zkpp, "", "    ")
	fmt.Println(string(data))

	isv, err := mpoints.VerifyZkProof(zkp.forkID, zkp.proof, zkp.pubSignals)
	require.NoError(t, err)
	require.Equal(t, true, isv)
}

func TestVerifyMainnet(t *testing.T) {
	cfg := etherman.Config{
		URL:       "https://mainnet-l1.merlinchain.io",
		L1ChainID: 202401,
		L2ChainID: 4200,
	}

	ethermanClient, err := ethclient.Dial(cfg.URL)
	require.NoError(t, err)

	conf := &ethconfig.Merlin{
		VerifyZkProofConfigs: []*ethconfig.VerifyZkProofConfig{{
			ForkID:            uint64(chain.ForkID8Elderberry),
			Verifier:          libcommon.HexToAddress("0x65f25cED51CfDe249f307Cf6fC60A9988D249A69"), //
			TrustedAggregator: libcommon.HexToAddress("0xe76cc099094d484e67cd7b777d22a93afc2920cc"), //
		}},
	}
	mpoints := NewMerlinAPI(nil, nil, conf, nil, newTestL1Syncer(ethermanClient))
	blockHash := libcommon.HexToHash("0xfb10076988cc8fee0ef51a2afd93c3433333d7977e2058e370a0b29eb52363dc") // 0x30f569
	txindex := uint(0)

	ret := newRemoteTest(mpoints, "http://43.134.127.80:9123", ethermanClient, blockHash, txindex, cfg.L2ChainID, uint64(chain.ForkID8Elderberry))
	mpoints.setRemote(ret)

	zkp, snark, err := mpoints.getZkProofMeta(context.Background(), blockHash, uint64(chain.ForkID8Elderberry))
	require.NoError(t, err)

	zkpp := ZKProof{
		ForkID:      zkp.forkID,
		Proof:       zkp.proof,
		PubSignals:  zkp.pubSignals,
		RpubSignals: &RawPubSignals{Snark: snark, Rfield: RFIELD},
	}
	print, _ := json.MarshalIndent(zkpp, "", "    ")
	fmt.Println(string(print))

	isv, err := mpoints.VerifyZkProof(zkp.forkID, zkp.proof, zkp.pubSignals)
	require.NoError(t, err)
	require.Equal(t, true, isv)
}

func TestVerifyMainnetForkID5(t *testing.T) {
	cfg := etherman.Config{
		URL:       "https://mainnet-l1.merlinchain.io",
		L2ChainID: 4200,
	}

	ethermanClient, err := ethclient.Dial(cfg.URL)
	require.NoError(t, err)
	conf := &ethconfig.Merlin{
		VerifyZkProofConfigs: []*ethconfig.VerifyZkProofConfig{{
			ForkID:            uint64(chain.ForkID5Dragonfruit),
			Verifier:          libcommon.HexToAddress("0x7d72cc8E89B187a93581ee44FB1884b498989A40"), //旧的 0x7d72cc8E89B187a93581ee44FB1884b498989A40  //新的 0x65f25cED51CfDe249f307Cf6fC60A9988D249A69
			TrustedAggregator: libcommon.HexToAddress("0xe76cc099094d484e67cd7b777d22a93afc2920cc"), //
		}},
	}

	mpoints := NewMerlinAPI(nil, nil, conf, nil, newTestL1Syncer(ethermanClient))

	blockHash := libcommon.HexToHash("0x7ddf342c18664476959ef43287fb3e206922f712e3aa31e1c395cac67c377351") //03
	txindex := uint(0)
	ret := newRemoteTest(mpoints, "http://43.134.127.80:9123", ethermanClient, blockHash, txindex, cfg.L2ChainID, uint64(chain.ForkID5Dragonfruit))
	mpoints.setRemote(ret)

	zkm, snark, err := mpoints.getZkProofMeta(context.Background(), blockHash, uint64(chain.ForkID5Dragonfruit))
	require.NoError(t, err)

	blr, errd := ret.getVerifyBlockNumRange(zkm.initNumBatch+1, zkm.finalNewBatch)
	require.NoError(t, errd)
	require.NotNil(t, blr)
	fmt.Println("start block", blr.(blockRange).start, "end block", blr.(blockRange).end)

	zkpp := ZKProof{
		ForkID:        zkm.forkID,
		Proof:         zkm.proof,
		PubSignals:    zkm.pubSignals,
		RpubSignals:   &RawPubSignals{Snark: snark, Rfield: RFIELD},
		StartBlockNum: blr.(blockRange).start,
		EndBlockNum:   blr.(blockRange).end,
	}
	print, _ := json.MarshalIndent(zkpp, "", "    ")
	fmt.Println(string(print))

	isv, err := mpoints.VerifyZkProof(zkm.forkID, zkm.proof, zkm.pubSignals)
	require.NoError(t, err)
	require.Equal(t, true, isv)
}

func newTestL1Syncer(client *ethclient.Client) *syncer.L1Syncer {
	return syncer.NewL1Syncer(
		ctx,
		[]syncer.IEtherman{client},
		[]libcommon.Address{},
		[][]libcommon.Hash{},
		10,
		0,
		"latest",
	)
}

func TestVerifyGetInputSnarkBytes(t *testing.T) {
	type testcase struct {
		input  *InputSnark
		result string
	}
	testcases := []*testcase{
		{
			input: &InputSnark{
				Sender:           libcommon.HexToAddress("0xe76cc099094d484e67cd7b777d22a93afc2920cc"),
				OldStateRoot:     libcommon.HexToHash("0xbc26b56bbd4fa7c91c97a0e0fea120b7d26eba75daa2cc3035b5edcc2b5c6630"),
				OldAccInputHash:  libcommon.HexToHash("0xab07cc71710e24d280bcd070abf25eb01b99788c985c9cd3ede196a5e9586672"),
				InitNumBatch:     1774053,
				ChainId:          4200,
				ForkID:           8,
				NewStateRoot:     libcommon.HexToHash("0x97b2f0666edfff8c6eb8315c0161db5a10ae11342ba7f34da46d581bcb70e376"),
				NewAccInputHash:  libcommon.HexToHash("0x0db4014d73587d6ef5f9dfabdc9a14ebafddeee91f6da5fba029f9f84bfd1631"),
				NewLocalExitRoot: libcommon.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000000"),
				FinalNewBatch:    1774057,
			},
			result: "0xe76cc099094d484e67cd7b777d22a93afc2920ccbc26b56bbd4fa7c91c97a0e0fea120b7d26eba75daa2cc3035b5edcc2b5c6630ab07cc71710e24d280bcd070abf25eb01b99788c985c9cd3ede196a5e958667200000000001b11e50000000000001068000000000000000897b2f0666edfff8c6eb8315c0161db5a10ae11342ba7f34da46d581bcb70e3760db4014d73587d6ef5f9dfabdc9a14ebafddeee91f6da5fba029f9f84bfd1631000000000000000000000000000000000000000000000000000000000000000000000000001b11e9",
		},
	}

	for _, tc := range testcases {
		snark, err := getInputSnarkBytes(tc.input)
		require.NoError(t, err)
		fmt.Println("snark", len(hex.EncodeToString(snark)), len(tc.result), hex.EncodeToString(snark))
		require.Equal(t, tc.result, hex.EncodeToHex(snark))
	}
}

func TestGetOldAccHash(t *testing.T) {
	mapKeyHex := fmt.Sprintf("%064x%064x", 1, 114 /* _legacySequencedBatches slot*/)
	mapKey := keccak256.Hash(common.FromHex(mapKeyHex))
	mkh := libcommon.BytesToHash(mapKey)
	fmt.Println(mkh)
}
