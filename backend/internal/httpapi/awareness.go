package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"dreamwhiteboard/backend/internal/domain"
)

const (
	maxAwarenessIDsPerConnection = 32
	maxSafeAwarenessInteger      = uint64(1<<53 - 1)
)

var errAwarenessIDConflict = errors.New("awareness ID is owned by another connection")

type awarenessOwnerKey struct {
	boardID     string
	awarenessID uint64
}

type awarenessRegistry struct {
	mu       sync.Mutex
	owners   map[awarenessOwnerKey]string
	byClient map[string]map[awarenessOwnerKey]struct{}
}

func newAwarenessRegistry() *awarenessRegistry {
	return &awarenessRegistry{
		owners:   make(map[awarenessOwnerKey]string),
		byClient: make(map[string]map[awarenessOwnerKey]struct{}),
	}
}

func (r *awarenessRegistry) claim(boardID, clientID string, awarenessIDs []uint64) error {
	if boardID == "" || clientID == "" {
		return errors.New("awareness owner is invalid")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	owned := r.byClient[clientID]
	additional := 0
	for _, awarenessID := range awarenessIDs {
		key := awarenessOwnerKey{boardID: boardID, awarenessID: awarenessID}
		if owner := r.owners[key]; owner != "" && owner != clientID {
			return errAwarenessIDConflict
		}
		if _, exists := owned[key]; !exists {
			additional++
		}
	}
	if len(owned)+additional > maxAwarenessIDsPerConnection {
		return fmt.Errorf("a connection may own at most %d awareness IDs", maxAwarenessIDsPerConnection)
	}
	if owned == nil {
		owned = make(map[awarenessOwnerKey]struct{})
		r.byClient[clientID] = owned
	}
	for _, awarenessID := range awarenessIDs {
		key := awarenessOwnerKey{boardID: boardID, awarenessID: awarenessID}
		r.owners[key] = clientID
		owned[key] = struct{}{}
	}
	return nil
}

func (r *awarenessRegistry) release(clientID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for key := range r.byClient[clientID] {
		if r.owners[key] == clientID {
			delete(r.owners, key)
		}
	}
	delete(r.byClient, clientID)
}

func decodeAwarenessClientIDs(update []byte) ([]uint64, error) {
	decoder := awarenessDecoder{data: update}
	entryCount, err := decoder.readVarUint()
	if err != nil {
		return nil, err
	}
	if entryCount > maxAwarenessIDsPerConnection {
		return nil, fmt.Errorf("awareness update contains too many entries")
	}
	ids := make([]uint64, 0, entryCount)
	seen := make(map[uint64]struct{}, entryCount)
	for index := uint64(0); index < entryCount; index++ {
		awarenessID, err := decoder.readVarUint()
		if err != nil {
			return nil, err
		}
		if _, err := decoder.readVarUint(); err != nil { // awareness clock
			return nil, err
		}
		state, err := decoder.readVarBytes()
		if err != nil {
			return nil, err
		}
		var decoded any
		if err := json.Unmarshal(state, &decoded); err != nil {
			return nil, errors.New("awareness state is not valid JSON")
		}
		if decoded != nil {
			if _, ok := decoded.(map[string]any); !ok {
				return nil, errors.New("awareness state must be an object or null")
			}
		}
		if _, exists := seen[awarenessID]; !exists {
			seen[awarenessID] = struct{}{}
			ids = append(ids, awarenessID)
		}
	}
	if decoder.offset != len(update) {
		return nil, errors.New("awareness update has trailing data")
	}
	return ids, nil
}

type awarenessDecoder struct {
	data   []byte
	offset int
}

func (d *awarenessDecoder) readVarUint() (uint64, error) {
	var value uint64
	var multiplier uint64 = 1
	for {
		if d.offset >= len(d.data) {
			return 0, errors.New("awareness update ended unexpectedly")
		}
		current := d.data[d.offset]
		d.offset++
		part := uint64(current & 0x7f)
		if part > (maxSafeAwarenessInteger-value)/multiplier {
			return 0, errors.New("awareness integer exceeds JavaScript safe range")
		}
		value += part * multiplier
		if current&0x80 == 0 {
			return value, nil
		}
		if multiplier > maxSafeAwarenessInteger/128 {
			return 0, errors.New("awareness integer exceeds JavaScript safe range")
		}
		multiplier *= 128
	}
}

func (d *awarenessDecoder) readVarBytes() ([]byte, error) {
	length, err := d.readVarUint()
	if err != nil {
		return nil, err
	}
	if length > uint64(len(d.data)-d.offset) {
		return nil, errors.New("awareness string exceeds update length")
	}
	value := d.data[d.offset : d.offset+int(length)]
	d.offset += int(length)
	return value, nil
}

func awarenessDisplayName(user domain.User) string {
	if name := strings.TrimSpace(user.Name); name != "" {
		return name
	}
	return user.Email
}
