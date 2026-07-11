package store

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"sort"
	"strings"
)

const (
	maxBoardAssetReferences = 10_000
	maxAssetReferenceIDSize = 128
)

// canonicalAssetIDs validates and canonicalizes a client-declared complete
// asset manifest. A nil slice is preserved because it denotes a legacy client
// that did not send a manifest, while an empty non-nil slice is a valid empty
// manifest.
func canonicalAssetIDs(assetIDs []string) ([]string, error) {
	if assetIDs == nil {
		return nil, nil
	}
	if len(assetIDs) > maxBoardAssetReferences {
		return nil, ErrInvalidAssetReference
	}
	unique := make(map[string]struct{}, len(assetIDs))
	for _, id := range assetIDs {
		if id == "" || len(id) > maxAssetReferenceIDSize || strings.TrimSpace(id) != id {
			return nil, ErrInvalidAssetReference
		}
		unique[id] = struct{}{}
	}
	canonical := make([]string, 0, len(unique))
	for id := range unique {
		canonical = append(canonical, id)
	}
	sort.Strings(canonical)
	return canonical, nil
}

func hashBoardUpdate(update []byte, referenceBaseSequence *int64, canonicalAssetIDs []string) string {
	// Preserve idempotent receipts written by protocol-v2 servers. The absence
	// of both reference fields is itself the legacy manifest state; any v3
	// manifest uses the domain-separated hash below.
	if referenceBaseSequence == nil && canonicalAssetIDs == nil {
		return hashUpdate(update)
	}
	hash := sha256.New()
	writeHashPart(hash, []byte("dreamwhiteboard-board-update-v3"))
	writeHashPart(hash, update)
	if canonicalAssetIDs == nil {
		writeHashPart(hash, []byte{0})
	} else {
		writeHashPart(hash, []byte{1})
		var sequence [8]byte
		if referenceBaseSequence != nil {
			binary.BigEndian.PutUint64(sequence[:], uint64(*referenceBaseSequence))
		}
		writeHashPart(hash, sequence[:])
		for _, id := range canonicalAssetIDs {
			writeHashPart(hash, []byte(id))
		}
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func hashAssetReferences(canonicalAssetIDs []string) string {
	hash := sha256.New()
	writeHashPart(hash, []byte("dreamwhiteboard-asset-references-v1"))
	for _, id := range canonicalAssetIDs {
		writeHashPart(hash, []byte(id))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

type hashWriter interface {
	Write([]byte) (int, error)
}

func writeHashPart(hash hashWriter, value []byte) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = hash.Write(size[:])
	_, _ = hash.Write(value)
}
