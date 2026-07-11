package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"dreamwhiteboard/backend/internal/domain"
	"dreamwhiteboard/backend/internal/filestore"
)

var allowedImageTypes = map[string]string{
	"image/gif":  ".gif",
	"image/jpeg": ".jpg",
	"image/png":  ".png",
}

func (s *Server) handleAssetUpload(w http.ResponseWriter, r *http.Request, user domain.User, projectID string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, r, http.MethodPost)
		return
	}
	allowed, permissionErr := s.canEditProject(user, projectID)
	if !s.requirePermission(w, r, allowed, permissionErr, "editor_required", "project editor access required") {
		return
	}
	// Keep only a small multipart prefix in memory; file bodies spill to the
	// process temp directory and are removed with MultipartForm.RemoveAll.
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeAPIError(w, r, http.StatusRequestEntityTooLarge, "upload_too_large", "uploaded file exceeds the configured size limit", nil)
		} else {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_multipart", "request must be a valid multipart upload", nil)
		}
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeAPIError(w, r, http.StatusUnprocessableEntity, "validation_failed", "file is required", map[string]string{"file": "file is required"})
		return
	}
	defer file.Close()
	if header.Size > s.config.MaxUploadBytes {
		writeAPIError(w, r, http.StatusRequestEntityTooLarge, "upload_too_large", "uploaded file exceeds the configured size limit", nil)
		return
	}

	fileName := safeFileName(header.Filename)
	if fileName == "" || len(fileName) > 255 {
		writeAPIError(w, r, http.StatusUnprocessableEntity, "validation_failed", "file name is invalid", map[string]string{"file": "file name must contain between 1 and 255 characters"})
		return
	}
	projectDir, err := filestore.ProjectPath(s.uploadDir, projectID)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_project_id", "project identifier is invalid", nil)
		return
	}
	if err := os.MkdirAll(projectDir, 0o750); err != nil {
		s.logger.Error("create upload directory", "request_id", requestID(r.Context()), "project_id", projectID, "error", err)
		writeAPIError(w, r, http.StatusInternalServerError, "upload_failed", "could not store uploaded file", nil)
		return
	}

	temp, err := os.CreateTemp(projectDir, ".upload-*")
	if err != nil {
		s.logger.Error("create temporary upload", "request_id", requestID(r.Context()), "project_id", projectID, "error", err)
		writeAPIError(w, r, http.StatusInternalServerError, "upload_failed", "could not store uploaded file", nil)
		return
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		writeAPIError(w, r, http.StatusInternalServerError, "upload_failed", "could not store uploaded file", nil)
		return
	}
	hasher := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(temp, hasher), io.LimitReader(file, s.config.MaxUploadBytes+1))
	syncErr := temp.Sync()
	closeErr := temp.Close()
	if size > s.config.MaxUploadBytes {
		writeAPIError(w, r, http.StatusRequestEntityTooLarge, "upload_too_large", "uploaded file exceeds the configured size limit", nil)
		return
	}
	if copyErr != nil || syncErr != nil || closeErr != nil {
		s.logger.Error("write upload", "request_id", requestID(r.Context()), "project_id", projectID, "copy_error", copyErr, "sync_error", syncErr, "close_error", closeErr)
		writeAPIError(w, r, http.StatusInternalServerError, "upload_failed", "could not store uploaded file", nil)
		return
	}

	contentType, width, height, err := inspectImage(tempPath, s.config.MaxImagePixels)
	if err != nil {
		writeAPIError(w, r, http.StatusUnsupportedMediaType, "unsupported_image", err.Error(), map[string]string{"file": err.Error()})
		return
	}
	extension := allowedImageTypes[contentType]
	storageKey := randomHex(24) + extension
	finalPath := filepath.Join(projectDir, storageKey)
	if err := os.Rename(tempPath, finalPath); err != nil {
		s.logger.Error("commit upload", "request_id", requestID(r.Context()), "project_id", projectID, "error", err)
		writeAPIError(w, r, http.StatusInternalServerError, "upload_failed", "could not store uploaded file", nil)
		return
	}
	committed := true
	defer func() {
		if committed {
			_ = os.Remove(finalPath)
		}
	}()

	asset, err := s.repo.SaveAsset(domain.Asset{
		ID:          randomID("ast"),
		ProjectID:   projectID,
		UploadedBy:  user.ID,
		FileName:    fileName,
		ContentType: contentType,
		Size:        size,
		Path:        finalPath,
		StorageKey:  storageKey,
		SHA256:      hex.EncodeToString(hasher.Sum(nil)),
		Width:       width,
		Height:      height,
	})
	if err != nil {
		s.logger.Error("persist upload metadata", "request_id", requestID(r.Context()), "project_id", projectID, "error", err)
		writeResult(w, r, nil, err)
		return
	}
	committed = false
	writeJSON(w, http.StatusCreated, asset)
}

func (s *Server) handleAsset(w http.ResponseWriter, r *http.Request, user domain.User) {
	parts := splitPath(strings.TrimPrefix(r.URL.Path, "/api/assets/"))
	if len(parts) != 1 {
		writeAPIError(w, r, http.StatusNotFound, "not_found", "resource not found", nil)
		return
	}
	asset, err := s.repo.GetAsset(parts[0])
	if err != nil {
		writeResult(w, r, nil, err)
		return
	}
	allowed, permissionErr := s.canViewProject(user, asset.ProjectID)
	if !s.requirePermission(w, r, allowed, permissionErr, "asset_access_denied", "asset access denied") {
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.serveAsset(w, r, asset)
	case http.MethodDelete:
		allowed, permissionErr := s.canManageProject(user, asset.ProjectID)
		if !s.requirePermission(w, r, allowed, permissionErr, "project_admin_required", "project administrator access required") {
			return
		}
		if err := s.repo.DeleteAsset(asset.ID); err != nil {
			writeResult(w, r, nil, err)
			return
		}
		if path, pathErr := s.assetPath(asset); pathErr == nil {
			if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				s.logger.Error("remove asset file", "request_id", requestID(r.Context()), "asset_id", asset.ID, "error", removeErr)
			}
		} else {
			s.logger.Error("resolve asset path for removal", "request_id", requestID(r.Context()), "asset_id", asset.ID, "error", pathErr)
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	default:
		methodNotAllowed(w, r, http.MethodGet, http.MethodDelete)
	}
}

func (s *Server) serveAsset(w http.ResponseWriter, r *http.Request, asset domain.Asset) {
	path, err := s.assetPath(asset)
	if err != nil {
		s.logger.Error("invalid asset storage path", "request_id", requestID(r.Context()), "asset_id", asset.ID, "error", err)
		writeAPIError(w, r, http.StatusNotFound, "not_found", "resource not found", nil)
		return
	}
	file, err := os.Open(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			s.logger.Error("open asset file", "request_id", requestID(r.Context()), "asset_id", asset.ID, "error", err)
		}
		writeAPIError(w, r, http.StatusNotFound, "not_found", "resource not found", nil)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "asset_read_failed", "could not read asset", nil)
		return
	}
	w.Header().Set("Content-Type", asset.ContentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": asset.FileName}))
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	http.ServeContent(w, r, asset.FileName, info.ModTime(), file)
}

func inspectImage(path string, maxPixels int64) (string, int, int, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, 0, errors.New("uploaded file could not be inspected")
	}
	defer file.Close()
	header := make([]byte, 512)
	n, readErr := io.ReadFull(file, header)
	if readErr != nil && !errors.Is(readErr, io.ErrUnexpectedEOF) && !errors.Is(readErr, io.EOF) {
		return "", 0, 0, errors.New("uploaded file could not be inspected")
	}
	contentType := http.DetectContentType(header[:n])
	if _, ok := allowedImageTypes[contentType]; !ok {
		return "", 0, 0, errors.New("only valid JPEG, PNG, and GIF images are supported")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", 0, 0, errors.New("uploaded file could not be inspected")
	}
	config, format, err := image.DecodeConfig(file)
	if err != nil || config.Width <= 0 || config.Height <= 0 {
		return "", 0, 0, errors.New("uploaded file is not a valid image")
	}
	expected := map[string]string{"gif": "image/gif", "jpeg": "image/jpeg", "png": "image/png"}[format]
	if expected != contentType {
		return "", 0, 0, errors.New("uploaded file content does not match its image type")
	}
	if int64(config.Width) > maxPixels/int64(config.Height) {
		return "", 0, 0, errors.New("image dimensions exceed the configured pixel limit")
	}
	return contentType, config.Width, config.Height, nil
}

func (s *Server) assetPath(asset domain.Asset) (string, error) {
	if asset.StorageKey != "" {
		return filestore.AssetPath(s.uploadDir, asset.ProjectID, asset.StorageKey)
	}
	if asset.Path == "" {
		return "", errors.New("asset has no storage path")
	}
	return filestore.EnsureWithin(s.uploadDir, asset.Path)
}

func (s *Server) cleanupProjectFiles(projectID string, r *http.Request) {
	projectDir, err := filestore.ProjectPath(s.uploadDir, projectID)
	if err != nil {
		s.logger.Error("resolve project upload directory for removal", "request_id", requestID(r.Context()), "project_id", projectID, "error", err)
		return
	}
	if err := os.RemoveAll(projectDir); err != nil {
		s.logger.Error("remove project upload directory", "request_id", requestID(r.Context()), "project_id", projectID, "error", err)
	}
}

func safeFileName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = filepath.Base(name)
	name = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name))
	return name
}
