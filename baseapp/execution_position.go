package baseapp

import sdk "github.com/cosmos/cosmos-sdk/types"

type transactionIndexKey struct{}
type messagePathKey struct{}

// TransactionIndex is the position in FinalizeBlock's ordered transaction list.
// It is absent outside block execution, including simulation and proposal checks.
func TransactionIndex(ctx sdk.Context) (uint32, bool) {
	index, ok := ctx.Value(transactionIndexKey{}).(uint32)
	return index, ok
}

// MessagePath returns an owned path through SDK message routing. Nested routers
// append their child position rather than replacing the outer message position.
func MessagePath(ctx sdk.Context) []uint32 {
	path, _ := ctx.Value(messagePathKey{}).([]uint32)
	return append([]uint32(nil), path...)
}

func WithMessagePosition(ctx sdk.Context, index uint32) sdk.Context {
	path := append(MessagePath(ctx), index)
	return ctx.WithValue(messagePathKey{}, path)
}
