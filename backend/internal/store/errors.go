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
)
