package rootmulti

import (
	"bytes"
	"fmt"
)

const commitMetadataPrefix = "commit_aux/"

type stagedCommitMetadata struct {
	height     int64
	key, value []byte
}

// StageCommitMetadata includes one external recovery record in the next normal
// Commit's decision batch. It is unversioned and must not be used as app state.
// Its authenticated digest belongs in a mounted consensus store.
func (rs *Store) StageCommitMetadata(height int64, key, value []byte) error {
	if rs.stagedMetadata != nil {
		return fmt.Errorf("commit metadata already staged")
	}
	if height <= 0 || !bytes.HasPrefix(key, []byte(commitMetadataPrefix)) || len(key) == len(commitMetadataPrefix) {
		return fmt.Errorf("invalid commit metadata height or private key")
	}
	rs.stagedMetadata = &stagedCommitMetadata{height: height, key: bytes.Clone(key), value: bytes.Clone(value)}
	return nil
}

// ReadCommitMetadata reads an unversioned recovery record. Snapshot import does
// not restore these records; consumers must verify their authenticated digest
// and boundary before using them.
func (rs *Store) ReadCommitMetadata(key []byte) ([]byte, error) {
	if !bytes.HasPrefix(key, []byte(commitMetadataPrefix)) {
		return nil, fmt.Errorf("invalid private commit metadata key")
	}
	return rs.db.Get(key)
}
