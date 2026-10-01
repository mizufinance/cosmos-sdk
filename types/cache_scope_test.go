package types_test

import (
	"errors"
	"testing"

	"cosmossdk.io/log"
	"cosmossdk.io/store/metrics"
	"cosmossdk.io/store/rootmulti"
	storetypes "cosmossdk.io/store/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
)

type externalScope struct {
	prepared, adopted, aborted bool
	failure                    error
}

func (s *externalScope) PrepareAdopt() error { s.prepared = true; return s.failure }
func (s *externalScope) Adopt()              { s.adopted = true }
func (s *externalScope) Abort() {
	if !s.adopted {
		s.aborted = true
	}
}

func TestExplicitCacheScopeDisposition(t *testing.T) {
	for _, mode := range []string{"commit", "discard", "prepare failure"} {
		t.Run(mode, func(t *testing.T) {
			db := dbm.NewMemDB()
			defer db.Close()
			ms := rootmulti.NewStore(db, log.NewNopLogger(), metrics.NewNoOpMetrics())
			key := storetypes.NewKVStoreKey("external")
			ms.MountStoreWithDB(key, storetypes.StoreTypeIAVL, nil)
			require.NoError(t, ms.LoadLatestVersion())
			ctx := sdk.NewContext(ms.CacheMultiStore(), cmtproto.Header{}, false, log.NewNopLogger())
			owner := new(externalScope)
			if mode == "prepare failure" {
				owner.failure = errors.New("unavailable ownership")
			}
			ctx = sdk.WithCacheScopeFactory(ctx, func(parent, child sdk.Context) (sdk.Context, sdk.CacheScope, error) { return child, owner, nil })
			child, write, actual, err := sdk.CacheContextWithScope(ctx)
			require.NoError(t, err)
			child.KVStore(key).Set([]byte("value"), []byte("changed"))
			switch mode {
			case "commit":
				write()
			case "prepare failure":
				require.Panics(t, write)
			}
			sdk.AbortCacheScope(actual)
			if mode == "commit" {
				require.True(t, owner.prepared && owner.adopted && !owner.aborted)
				require.Equal(t, []byte("changed"), ctx.KVStore(key).Get([]byte("value")))
			} else {
				require.True(t, owner.aborted && !owner.adopted)
				require.Nil(t, ctx.KVStore(key).Get([]byte("value")))
			}
		})
	}
}
