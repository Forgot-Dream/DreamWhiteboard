package filestore

import (
	"errors"
	"path/filepath"
	"strings"
)

func ProjectPath(root, projectID string) (string, error) {
	return SafeJoin(root, projectID)
}

func AssetPath(root, projectID, storageKey string) (string, error) {
	projectDir, err := ProjectPath(root, projectID)
	if err != nil {
		return "", err
	}
	return SafeJoin(projectDir, storageKey)
}

func SafeJoin(root, segment string) (string, error) {
	if segment == "" || segment == "." || segment == ".." || filepath.Base(segment) != segment || strings.ContainsAny(segment, `/\`) {
		return "", errors.New("unsafe storage path")
	}
	return EnsureWithin(root, filepath.Join(root, segment))
}

func EnsureWithin(root, path string) (string, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	pathAbs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(rootAbs, pathAbs)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("storage path escapes upload directory")
	}
	return pathAbs, nil
}
