package baseapp_test

import (
	"context"
	"errors"
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/cosmos/cosmos-sdk/baseapp"
	baseapptestutil "github.com/cosmos/cosmos-sdk/baseapp/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
)

type nativeScopeKey struct{}

type nativeMessageScope struct {
	value           int
	committed       *int
	closed, adopted bool
	prepareErr      error
	adoptPanic      bool
}

func (scope *nativeMessageScope) PrepareAdopt() error { return scope.prepareErr }
func (scope *nativeMessageScope) Adopt() {
	if scope.adoptPanic {
		panic("injected external adoption failure")
	}
	*scope.committed = scope.value
	scope.adopted, scope.closed = true, true
}
func (scope *nativeMessageScope) Abort() { scope.closed = true }

func TestMessageCacheDisposition(t *testing.T) {
	for _, failure := range []string{"none", "later-message", "post-handler", "handler-panic", "block-gas", "prepare", "adopt-panic"} {
		t.Run(failure, func(t *testing.T) {
			committed := 0
			var scope *nativeMessageScope
			suite := NewBaseAppSuite(t, func(app *baseapp.BaseApp) {
				app.SetMessageCacheHook(func(parent, child sdk.Context, mode baseapp.MessageCacheMode) (sdk.Context, baseapp.MessageCacheScope, error) {
					require.Equal(t, baseapp.MessageCacheFinalize, mode)
					scope = &nativeMessageScope{committed: &committed}
					if failure == "prepare" {
						scope.prepareErr = errors.New("prepare rejected")
					}
					scope.adoptPanic = failure == "adopt-panic"
					return child.WithValue(nativeScopeKey{}, scope), scope, nil
				})
				app.SetPostHandler(func(ctx sdk.Context, _ sdk.Tx, _, _ bool) (sdk.Context, error) {
					if failure == "post-handler" {
						return ctx, errors.New("post-handler rejected")
					}
					return ctx, nil
				})
			})
			calls := 0
			baseapptestutil.RegisterCounterServer(suite.baseApp.MsgServiceRouter(), mockCounterServer{
				incrementCounterFn: func(ctx context.Context, _ *baseapptestutil.MsgCounter) (*baseapptestutil.MsgCreateCounterResponse, error) {
					calls++
					sdkCtx := sdk.UnwrapSDKContext(ctx)
					native := sdkCtx.Value(nativeScopeKey{}).(*nativeMessageScope)
					native.value++
					sdkCtx.KVStore(capKey1).Set([]byte("paired"), []byte("written"))
					if failure == "handler-panic" {
						panic("handler panic")
					}
					if failure == "later-message" && calls == 2 {
						return nil, errors.New("second message rejected")
					}
					if failure == "block-gas" {
						sdkCtx.GasMeter().ConsumeGas(2, "external effect")
					}
					return &baseapptestutil.MsgCreateCounterResponse{}, nil
				},
			})
			params := &cmtproto.ConsensusParams{}
			if failure == "block-gas" {
				params.Block = &cmtproto.BlockParams{MaxGas: 1}
			}
			_, err := suite.baseApp.InitChain(&abci.RequestInitChain{ConsensusParams: params})
			require.NoError(t, err)
			tx := newTxCounter(t, suite.txConfig, 0, 0)
			if failure == "later-message" {
				tx = newTxCounter(t, suite.txConfig, 0, 0, 0)
			}
			encoded, err := suite.txConfig.TxEncoder()(tx)
			require.NoError(t, err)
			request := &abci.RequestFinalizeBlock{Height: 1, Txs: [][]byte{encoded}}
			if failure == "adopt-panic" {
				require.Panics(t, func() { _, _ = suite.baseApp.FinalizeBlock(request) })
			} else {
				response, err := suite.baseApp.FinalizeBlock(request)
				require.NoError(t, err)
				require.Equal(t, failure == "none", response.TxResults[0].IsOK())
			}
			require.NotNil(t, scope)
			require.True(t, scope.closed)
			require.Equal(t, failure == "none", scope.adopted)
			if failure == "none" {
				require.Equal(t, 1, committed)
			} else {
				require.Zero(t, committed)
			}
			if failure != "adopt-panic" {
				stored := suite.baseApp.GetContextForFinalizeBlock(nil).KVStore(capKey1).Get([]byte("paired"))
				require.Equal(t, failure == "none", stored != nil)
			}
		})
	}
}

func TestPublicQueryBoundaryDoesNotGateInternalSnapshotContexts(t *testing.T) {
	suite := NewBaseAppSuite(t)
	_, err := suite.baseApp.InitChain(&abci.RequestInitChain{ConsensusParams: &cmtproto.ConsensusParams{}})
	require.NoError(t, err)
	_, err = suite.baseApp.FinalizeBlock(&abci.RequestFinalizeBlock{Height: 1})
	require.NoError(t, err)
	_, err = suite.baseApp.Commit()
	require.NoError(t, err)
	seen := int64(-1)
	suite.baseApp.SetQueryBoundaryHook(func(ctx context.Context, h int64) error {
		seen = h
		return errors.New("native materialization pending")
	})
	// Internal SDK snapshot reservation must remain available before Commit ACK.
	_, err = suite.baseApp.CreateQueryContext(1, false)
	require.NoError(t, err)
	require.EqualValues(t, -1, seen)
	response, err := suite.baseApp.Query(context.Background(), &abci.RequestQuery{Path: "/store/key1/key", Data: []byte("paired")})
	require.NoError(t, err)
	require.EqualValues(t, 1, seen)
	require.Contains(t, response.Log, "native materialization pending")
}
