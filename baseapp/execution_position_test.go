package baseapp

import (
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
)

func TestExecutionPositionOwnership(t *testing.T) {
	ctx := sdk.Context{}.WithContext(t.Context())
	_, present := TransactionIndex(ctx)
	require.False(t, present)
	ctx = ctx.WithValue(transactionIndexKey{}, uint32(7))
	index, present := TransactionIndex(ctx)
	require.True(t, present)
	require.Equal(t, uint32(7), index)
	outer := WithMessagePosition(ctx, 2)
	inner := WithMessagePosition(outer, 4)
	require.Equal(t, []uint32{2}, MessagePath(outer))
	require.Equal(t, []uint32{2, 4}, MessagePath(inner))
	path := MessagePath(inner)
	path[0] = 99
	require.Equal(t, []uint32{2, 4}, MessagePath(inner))
}
