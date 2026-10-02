package rootmulti

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/syndtr/goleveldb/leveldb/opt"
	"os"
	"os/exec"
	"path/filepath"
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

type crashDecisionDB struct {
	dbm.DB
	phase string
	armed bool
}
type crashDecisionBatch struct {
	dbm.Batch
	owner   *crashDecisionDB
	receipt bool
}

func (d *crashDecisionDB) NewBatch() dbm.Batch {
	return &crashDecisionBatch{Batch: d.DB.NewBatch(), owner: d}
}
func (b *crashDecisionBatch) Set(key, value []byte) error {
	if bytes.Equal(key, []byte("commit_aux/shieldd/replay/latest")) {
		b.receipt = true
	}
	return b.Batch.Set(key, value)
}
func (b *crashDecisionBatch) WriteSync() error {
	if b.owner.armed && b.receipt && b.owner.phase == "before" {
		os.Exit(81)
	}
	err := b.Batch.WriteSync()
	if err == nil && b.owner.armed && b.receipt && b.owner.phase == "after" {
		os.Exit(81)
	}
	return err
}

// Abrupt process exit across a real rotated WAL: the SDK decision height and
// receipt must recover together. This does not simulate a filesystem power loss.
func TestCommitReceiptCrashAtomicityAcrossWALRotation(t *testing.T) {
	const child = "SDK_RECEIPT_CRASH_CHILD"
	if directory := os.Getenv(child); directory != "" {
		database, err := dbm.NewGoLevelDBWithOpts("application", directory, &opt.Options{WriteBuffer: 4 * 1024})
		require.NoError(t, err)
		db := &crashDecisionDB{DB: database, phase: os.Getenv("SDK_RECEIPT_CRASH_PHASE")}
		store := NewStore(db, log.NewNopLogger(), metrics.NewNoOpMetrics())
		require.NoError(t, store.LoadLatestVersion())
		require.NoError(t, store.StageCommitMetadata(1, []byte("commit_aux/shieldd/replay/latest"), []byte("receipt-1")))
		store.Commit()
		for i := 0; i < 200; i++ {
			require.NoError(t, database.Set([]byte(fmt.Sprintf("filler/%04d", i)), bytes.Repeat([]byte{byte(i)}, 4096)))
		}
		require.NoError(t, store.StageCommitMetadata(2, []byte("commit_aux/shieldd/replay/latest"), bytes.Repeat([]byte("receipt-2"), 8192)))
		db.armed = true
		store.Commit()
		t.Fatal("crash boundary was not reached")
	}
	for _, phase := range []string{"before", "after"} {
		t.Run(phase, func(t *testing.T) {
			directory := t.TempDir()
			process := exec.Command(os.Args[0], "-test.run=^TestCommitReceiptCrashAtomicityAcrossWALRotation$")
			process.Env = append(os.Environ(), child+"="+directory, "SDK_RECEIPT_CRASH_PHASE="+phase)
			output, err := process.CombinedOutput()
			var exit *exec.ExitError
			require.ErrorAs(t, err, &exit, string(output))
			require.Equal(t, 81, exit.ExitCode(), string(output))
			files, err := filepath.Glob(filepath.Join(directory, "application.db", "*.ldb"))
			require.NoError(t, err)
			require.NotEmpty(t, files, "fixture must rotate its WAL into SST files")
			database, err := dbm.NewGoLevelDBWithOpts("application", directory, &opt.Options{WriteBuffer: 4 * 1024})
			require.NoError(t, err)
			defer database.Close()
			store := NewStore(database, log.NewNopLogger(), metrics.NewNoOpMetrics())
			require.NoError(t, store.LoadLatestVersion())
			receipt, err := store.ReadCommitMetadata([]byte("commit_aux/shieldd/replay/latest"))
			require.NoError(t, err)
			if phase == "before" {
				require.EqualValues(t, 1, store.LastCommitID().Version)
				require.Equal(t, []byte("receipt-1"), receipt)
			} else {
				require.EqualValues(t, 2, store.LastCommitID().Version)
				require.Equal(t, bytes.Repeat([]byte("receipt-2"), 8192), receipt)
			}
		})
	}
}
