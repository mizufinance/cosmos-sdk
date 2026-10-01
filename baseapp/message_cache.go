package baseapp

import sdk "github.com/cosmos/cosmos-sdk/types"

// MessageCacheMode identifies whether the SDK will adopt a message cache.
type MessageCacheMode uint8

const (
	MessageCacheReadOnly MessageCacheMode = iota
	MessageCacheSimulate
	MessageCacheFinalize
)

// MessageCacheScope pairs an external effect cache with the SDK message cache.
// Abort must be idempotent after adoption. Adopt must not fail once PrepareAdopt
// succeeds; an unexpected adoption or cleanup panic terminates the candidate.
type MessageCacheScope interface {
	PrepareAdopt() error
	Adopt()
	Abort()
}

// MessageCacheHook binds the child context to the same backing multistore.
// Ante-handler effects retain the SDK's existing independent disposition.
type MessageCacheHook func(parent, child sdk.Context, mode MessageCacheMode) (sdk.Context, MessageCacheScope, error)

// FatalCachePanic escapes transaction panic recovery: external and SDK caches
// cannot safely continue after an unexpected adoption or cleanup failure.
type FatalCachePanic struct{ Cause any }

func (FatalCachePanic) FatalExecution() {}

func (app *BaseApp) SetMessageCacheHook(hook MessageCacheHook) {
	if app.sealed {
		panic("SetMessageCacheHook on sealed BaseApp")
	}
	app.messageCacheHook = hook
}

func messageCacheMode(mode execMode) MessageCacheMode {
	switch mode {
	case execModeFinalize:
		return MessageCacheFinalize
	case execModeSimulate:
		return MessageCacheSimulate
	default:
		return MessageCacheReadOnly
	}
}

func mustCompleteCache(action func()) {
	defer func() {
		if cause := recover(); cause != nil {
			panic(FatalCachePanic{Cause: cause})
		}
	}()
	action()
}
