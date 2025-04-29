package jsonrpc

import (
	"context"
	"math/big"
	"slices"
	"sync"
	"time"

	"github.com/ledgerwatch/erigon-lib/gointerfaces/txpool"
	"github.com/ledgerwatch/erigon-lib/kv"
	"github.com/ledgerwatch/erigon/core/types"
	"github.com/ledgerwatch/erigon/eth/ethconfig"
	"github.com/ledgerwatch/erigon/rpc"
	"github.com/ledgerwatch/erigon/turbo/rpchelper"
	"github.com/ledgerwatch/log/v3"
	"google.golang.org/protobuf/types/known/emptypb"

	proto_txpool "github.com/ledgerwatch/erigon-lib/gointerfaces/txpool"
)

const (
	sampleNumber = 3 // Number of transactions sampled in a block.
)

// L2GasPricer l2 gas pricer
type L2GasPricer interface {
	GetGasPrice() *big.Int
	UpdateGasPriceAvg()
}

// NewL2GasPricer new l2 gas pricer
func NewL2GasPricer(ctx context.Context, cfg *ethconfig.GasPriceConf, base *BaseAPI, txPool txpool.TxpoolClient, db kv.RoDB) L2GasPricer {
	pricer := newLastNL2BlocksGasPriceSuggester(ctx, cfg, base, txPool, db)
	go func() {
		up := cfg.UpdatePeriod
		updateTimer := time.NewTimer(up)
		for {
			select {
			case <-ctx.Done():
				log.Info("Finishing l2 gas pricer...")
				return
			case <-updateTimer.C:
				pricer.UpdateGasPriceAvg()
				updateTimer.Reset(up)
			}
		}
	}()
	return pricer
}

// LastNL2BlocksGasPrice struct for gas price estimator last n l2 blocks.
type LastNL2BlocksGasPrice struct {
	*BaseAPI
	cfg    *ethconfig.GasPriceConf
	ctx    context.Context
	txPool txpool.TxpoolClient
	db     kv.RoDB

	lastL2BlockNumber uint64
	lastPrice         *big.Int
	maxPrice          *big.Int
	minPrice          *big.Int
	ignorePrice       *big.Int

	cacheLock sync.RWMutex
	fetchLock sync.Mutex
}

// newLastNL2BlocksGasPriceSuggester init gas price suggester for last n l2 blocks strategy.
func newLastNL2BlocksGasPriceSuggester(ctx context.Context, cfg *ethconfig.GasPriceConf, base *BaseAPI,
	txPool txpool.TxpoolClient, db kv.RoDB) *LastNL2BlocksGasPrice {
	return &LastNL2BlocksGasPrice{
		BaseAPI:     base,
		cfg:         cfg,
		ctx:         ctx,
		txPool:      txPool,
		db:          db,
		lastPrice:   big.NewInt(0).SetUint64(cfg.DefaultGasPrice),
		maxPrice:    big.NewInt(0).SetUint64(cfg.MaxGasPrice),
		minPrice:    big.NewInt(0).SetUint64(cfg.DefaultGasPrice),
		ignorePrice: big.NewInt(0).SetUint64(cfg.IgnorePrice),
	}
}

// UpdateGasPriceAvg for last n blocks strategy is not needed to implement this function.
func (g *LastNL2BlocksGasPrice) UpdateGasPriceAvg() {
	l2BlockNumber, err := g.BlockNumber(g.ctx)
	if err != nil {
		log.Error("failed to get last l2 block number", "error", err)
	}
	g.cacheLock.RLock()
	lastL2BlockNumber, lastPrice := g.lastL2BlockNumber, g.lastPrice
	g.cacheLock.RUnlock()
	if l2BlockNumber == lastL2BlockNumber {
		log.Debug("Block is still the same, no need to update the gas price at the moment")
		return
	}

	g.fetchLock.Lock()
	defer g.fetchLock.Unlock()

	var (
		sent, exp int
		number    = l2BlockNumber
		result    = make(chan results, g.cfg.CheckBlocks)
		quit      = make(chan struct{})
		results   []*big.Int
	)

	for sent < g.cfg.CheckBlocks && number > 0 {
		go g.getL2BlockTxsTips(g.ctx, number, sampleNumber, g.ignorePrice, result, quit)
		sent++
		exp++
		number--
	}

	for exp > 0 {
		res := <-result
		if res.err != nil {
			close(quit)
			return
		}
		exp--

		if len(res.values) == 0 {
			res.values = []*big.Int{lastPrice}
		}
		results = append(results, res.values...)
	}

	price := lastPrice
	if len(results) > 0 {
		slices.SortFunc(results, func(a, b *big.Int) int { return a.Cmp(b) })
		price = results[(len(results)-1)*g.cfg.Percentile/100]
	}
	log.Debug("Gasprice historical data spot check results", "len(results):", len(results), "percentile:",
		g.cfg.Percentile, "checkBlocks:", g.cfg.CheckBlocks, "price:", price.Uint64())

	if g.cfg.EnableGasPriceDynamicDecay {
		isIdle, err := g.IsTxPoolIdle(g.ctx)
		if err != nil {
			log.Error("Unable to calculate the number of pool transactions pending by status", "error", err)
			return
		}
		// Dynamically reduce gas prices based on transaction pool idleness
		if isIdle {
			price = big.NewInt(int64(float64(price.Uint64()) * (1 - g.cfg.GasPriceDynamicDecayFactor)))
			log.Debug("Gas price dynamic decay", "before:", g.lastPrice, "after:", price)
		}
	}

	if g.cfg.MaxGasPrice > 0 && price.Cmp(g.maxPrice) > 0 {
		price = g.maxPrice
	}
	if price.Cmp(g.minPrice) < 0 {
		price = g.minPrice
	}

	g.cacheLock.Lock()
	g.lastPrice = price
	g.lastL2BlockNumber = l2BlockNumber
	g.cacheLock.Unlock()

	log.Debug("Setting gas prices", "block: ", g.lastL2BlockNumber, "l2 gas price:", g.lastPrice)
}

// GetGasPrice get gas price
func (g *LastNL2BlocksGasPrice) GetGasPrice() *big.Int {
	g.cacheLock.Lock()
	defer g.cacheLock.Unlock()
	return g.lastPrice
}

// IsTxPoolIdle check is tx pool idle
func (g *LastNL2BlocksGasPrice) IsTxPoolIdle(ctx context.Context) (bool, error) {
	sReply, err := g.txPool.Status(ctx, &proto_txpool.StatusRequest{})
	if err != nil {
		log.Error("failed to count pool txs by status pending", "error", err)
		return false, err
	}
	thresholdCount := uint64(float64(g.cfg.GlobalPending) * g.cfg.GlobalPendingDynamicFactor)
	if uint64(sReply.PendingCount) >= thresholdCount {
		return false, nil
	}

	reply, err := g.txPool.Pending(ctx, &emptypb.Empty{})
	if err != nil {
		return false, err
	}
	totalPendingGas := uint64(0)
	for _, rtx := range reply.Txs {
		txn, err := types.DecodeWrappedTransaction(rtx.RlpTx)
		if err != nil {
			continue
		}
		totalPendingGas += txn.GetGas()
	}

	isIdle := uint64(sReply.PendingCount) < thresholdCount && totalPendingGas < g.cfg.PendingGasLimit
	log.Debug("IsTxPoolIdle", "is", isIdle,
		"pendingCount:", sReply.PendingCount, "thresholdCount", thresholdCount,
		"totalPendingGas", totalPendingGas, "pendingGasLimit", g.cfg.PendingGasLimit)

	return isIdle, nil
}

// BlockNumber get current blocknum
func (g *LastNL2BlocksGasPrice) BlockNumber(ctx context.Context) (uint64, error) {
	tx, err := g.db.BeginRo(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	blockNum, err := rpchelper.GetLatestFinishedBlockNumber(tx)
	if err != nil {
		return 0, err
	}
	return blockNum, nil
}

// getL2BlockTxsTips calculates l2 block transaction gas fees.
func (g *LastNL2BlocksGasPrice) getL2BlockTxsTips(ctx context.Context, l2BlockNumber uint64, limit int, ignorePrice *big.Int, result chan results, quit chan struct{}) {
	txs, err := g.GetTxsByBlockNumber(ctx, rpc.BlockNumber(l2BlockNumber))
	if len(txs) == 0 {
		select {
		case result <- results{nil, err}:
		case <-quit:
		}
		return
	}

	sortedTxs := make([]types.Transaction, len(txs))
	copy(sortedTxs, txs)
	slices.SortFunc(sortedTxs, func(a, b types.Transaction) int {
		return a.GetTip().Cmp(b.GetTip())
	})

	var prices []*big.Int
	for _, tx := range sortedTxs {
		tip := tx.GetTip()
		if ignorePrice != nil && tip.ToBig().Cmp(ignorePrice) == -1 {
			continue
		}
		prices = append(prices, tip.ToBig())
		if len(prices) >= limit {
			break
		}
	}
	select {
	case result <- results{prices, nil}:
	case <-quit:
	}
}

// GetTxsByBlockNumber get txs by blocknumber
func (g *LastNL2BlocksGasPrice) GetTxsByBlockNumber(ctx context.Context, number rpc.BlockNumber) (types.Transactions, error) {
	tx, err := g.db.BeginRo(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	blk, err := g.BaseAPI.blockByRPCNumber(ctx, number, tx)
	if err != nil {
		return nil, err
	}
	return blk.Transactions(), nil
}

type results struct {
	values []*big.Int
	err    error
}
