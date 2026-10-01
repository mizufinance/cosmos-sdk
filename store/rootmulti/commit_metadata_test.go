package rootmulti

import (
	"errors"
	"testing"

	"cosmossdk.io/log"
	"cosmossdk.io/store/metrics"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/stretchr/testify/require"
)

type metadataBatchDB struct {
	dbm.DB
	writes []map[string][]byte
	fail   bool
}

func (db *metadataBatchDB) NewBatch() dbm.Batch {
	return &metadataBatch{Batch: db.DB.NewBatch(), owner: db, keys: make(map[string][]byte)}
}

type metadataBatch struct {
	dbm.Batch
	owner *metadataBatchDB
	keys  map[string][]byte
}

func (batch *metadataBatch) Set(key, value []byte) error {
	batch.keys[string(key)] = append([]byte(nil), value...)
	return batch.Batch.Set(key, value)
}

func (batch *metadataBatch) WriteSync() error {
	if batch.owner.fail {
		return errors.New("injected decision batch failure")
	}
	batch.owner.writes = append(batch.owner.writes, batch.keys)
	return batch.Batch.WriteSync()
}

func TestCommitMetadataDecisionBatch(t *testing.T) {
	db := &metadataBatchDB{DB: dbm.NewMemDB()}
	store := NewStore(db, log.NewNopLogger(), metrics.NewNoOpMetrics())
	require.NoError(t, store.LoadLatestVersion())
	key, value := []byte("commit_aux/shieldd/replay/latest"), []byte("receipt")
	require.NoError(t, store.StageCommitMetadata(1, key, value))
	require.Error(t, store.StageCommitMetadata(1, key, value))
	key[0], value[0] = 'X', 'X'
	store.Commit()
	require.Nil(t, store.stagedMetadata)
	var decisions int
	for _, write := range db.writes {
		if record, ok := write["commit_aux/shieldd/replay/latest"]; ok {
			decisions++
			require.Equal(t, []byte("receipt"), record)
			require.Contains(t, write, "s/latest")
			require.Contains(t, write, "s/1")
		}
	}
	require.Equal(t, 1, decisions)

	// Restore/rollback share this helper; neither writes nor consumes staging.
	require.NoError(t, store.StageCommitMetadata(2, []byte("commit_aux/shieldd/replay/latest"), []byte("next")))
	store.flushMetadata(db, 1, store.lastCommitInfo)
	require.NotNil(t, store.stagedMetadata)
	read, err := store.ReadCommitMetadata([]byte("commit_aux/shieldd/replay/latest"))
	require.NoError(t, err)
	require.Equal(t, []byte("receipt"), read)
}

func TestCommitMetadataFailures(t *testing.T) {
	for _, mismatch := range []bool{true, false} {
		db := &metadataBatchDB{DB: dbm.NewMemDB()}
		store := NewStore(db, log.NewNopLogger(), metrics.NewNoOpMetrics())
		require.NoError(t, store.LoadLatestVersion())
		require.Error(t, store.StageCommitMetadata(1, []byte("s/latest"), []byte("bad")))
		height := int64(1)
		if mismatch {
			height = 2
		} else {
			db.fail = true
		}
		require.NoError(t, store.StageCommitMetadata(height, []byte("commit_aux/shieldd/replay/latest"), []byte("receipt")))
		require.Panics(t, func() { store.Commit() })
		require.Nil(t, store.stagedMetadata)
		got, err := db.Get([]byte("commit_aux/shieldd/replay/latest"))
		require.NoError(t, err)
		require.Nil(t, got)
	}
}
