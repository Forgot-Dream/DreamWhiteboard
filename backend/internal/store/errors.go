package store

import "errors"

var (
	ErrNotFound                  = errors.New("not found")
	ErrConflict                  = errors.New("conflict")
	ErrForbidden                 = errors.New("forbidden")
	ErrInvalidInput              = errors.New("invalid input")
	ErrInvalidCredentials        = errors.New("invalid credentials")
	ErrLastOwner                 = errors.New("cannot remove or demote the last project owner")
	ErrLastSystemAdmin           = errors.New("cannot demote the last system administrator")
	ErrInvalidCheckpointSequence = errors.New("invalid checkpoint sequence")
	ErrUpdateIDConflict          = errors.New("update id was already used for different content")
	ErrInvalidAssetReference     = errors.New("asset reference is invalid for this board")
	ErrAssetReferenceIndexStale  = errors.New("asset reference index is stale")
	ErrAssetReferenceConflict    = errors.New("asset reference index is conflicted")
	ErrAssetInUse                = errors.New("asset is referenced by a board")
)
