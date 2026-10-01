package types

// CacheScope owns external effects corresponding to one explicitly instrumented
// SDK cache. Prepare must precede SDK writes; adoption follows the complete write.
type CacheScope interface {
	PrepareAdopt() error
	Adopt()
	Abort()
}

type CacheScopeFactory func(parent, child Context) (Context, CacheScope, error)
type cacheScopeFactoryKey struct{}

func WithCacheScopeFactory(ctx Context, factory CacheScopeFactory) Context {
	return ctx.WithValue(cacheScopeFactoryKey{}, factory)
}

type cacheScopePanic struct{ Cause any }

func (cacheScopePanic) FatalExecution() {}

func adoptCache(action func()) {
	defer func() {
		if cause := recover(); cause != nil {
			panic(cacheScopePanic{Cause: cause})
		}
	}()
	action()
}

// CacheContextWithScope is opt-in at cache owners that can reach external state.
// Every caller must defer AbortCacheScope, including caches that are never written.
// Plain CacheContext deliberately does not inherit an external writable capability.
func CacheContextWithScope(ctx Context) (Context, func(), CacheScope, error) {
	child, write := ctx.CacheContext()
	factory, _ := ctx.Value(cacheScopeFactoryKey{}).(CacheScopeFactory)
	if factory == nil {
		return child, write, nil, nil
	}
	child, owner, err := factory(ctx, child)
	if err != nil {
		AbortCacheScope(owner)
		return child, nil, nil, err
	}
	if owner == nil {
		panic(cacheScopePanic{Cause: "cache factory returned no owner"})
	}
	return child, func() {
		if err := owner.PrepareAdopt(); err != nil {
			panic(cacheScopePanic{Cause: err})
		}
		adoptCache(write)
		adoptCache(owner.Adopt)
	}, owner, nil
}

func AbortCacheScope(owner CacheScope) {
	if owner != nil {
		adoptCache(owner.Abort)
	}
}
