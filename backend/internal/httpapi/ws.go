package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"dreamwhiteboard/backend/internal/domain"
	"dreamwhiteboard/backend/internal/realtime"
	"dreamwhiteboard/backend/internal/store"
)

const (
	wsWriteWait          = 10 * time.Second
	wsPongWait           = 60 * time.Second
	wsPingPeriod         = (wsPongWait * 9) / 10
	wsMaxMessageBytes    = 24 << 20
	wsMaxUpdateBytes     = 8 << 20
	wsMaxCheckpointBytes = 16 << 20
	wsMaxAwarenessBytes  = 64 << 10
	wsSendBuffer         = 128
	wsMaxUpdateIDLength  = 128
	wsCheckpointIDPrefix = "cpr_"
	wsCheckpointIDHexLen = 32
	wsCloseHandshakeWait = time.Second
)

var boardWSUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
}

type wsClientMessage struct {
	Type                  string   `json:"type"`
	UpdateID              string   `json:"update_id"`
	RequestID             string   `json:"request_id"`
	ThroughSequence       int64    `json:"through_sequence"`
	ReferenceBaseSequence *int64   `json:"reference_base_sequence"`
	AssetIDs              []string `json:"asset_ids"`
	IntroducedAssetIDs    []string `json:"introduced_asset_ids"`
	Data                  []byte   `json:"data"`
}

func (s *Server) handleBoardWS(w http.ResponseWriter, r *http.Request, user domain.User, board domain.Board) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		writeAPIError(w, r, http.StatusUnauthorized, "authentication_required", "authentication required", nil)
		return
	}
	sessionHash := store.HashSessionToken(cookie.Value)
	upgrader := boardWSUpgrader
	upgrader.CheckOrigin = s.websocketOriginAllowed
	connection, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	connection.SetReadLimit(wsMaxMessageBytes)
	if err := connection.SetReadDeadline(time.Now().Add(wsPongWait)); err != nil {
		_ = connection.Close()
		return
	}
	connection.SetPongHandler(func(string) error {
		return connection.SetReadDeadline(time.Now().Add(wsPongWait))
	})

	canView, canEdit, canManage, err := s.projectPermissions(user, board.ProjectID)
	if err != nil {
		_ = writeWSJSON(connection, realtime.Message{Type: realtime.MessageError, Code: "authorization_unavailable", Message: "could not verify board permission"})
		_ = connection.Close()
		return
	}
	if !canView {
		_ = writeWSJSON(connection, realtime.Message{Type: realtime.MessageError, Code: "forbidden", Message: "board access was revoked"})
		_ = connection.Close()
		return
	}
	client := realtime.NewClient(randomID("cli"), user.ID, board.ID, canEdit, wsSendBuffer)
	client.CanCheckpoint = canManage
	if err := s.hub.Join(client, func() (realtime.SyncState, error) {
		return s.writeBoardSync(connection, user, board, client)
	}); err != nil {
		_ = writeWSJSON(connection, realtime.Message{
			Type:    realtime.MessageError,
			Code:    "sync_failed",
			Message: "could not synchronize board",
		})
		_ = connection.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "sync failed"),
			time.Now().Add(wsWriteWait),
		)
		_ = connection.Close()
		return
	}
	if err := connection.SetReadDeadline(time.Now().Add(wsPongWait)); err != nil {
		s.hub.Leave(client)
		_ = connection.Close()
		return
	}

	writeDone := make(chan struct{})
	go writeBoardWS(connection, client, writeDone)
	authStop := make(chan struct{})
	authDone := make(chan struct{})
	go s.monitorBoardWSAuthorization(client, board, sessionHash, authStop, authDone)
	defer func() {
		close(authStop)
		<-authDone
		s.hub.Leave(client)
		s.awareness.release(client.ID)
		select {
		case <-writeDone:
		case <-time.After(wsCloseHandshakeWait):
			_ = connection.Close()
		}
	}()

	for {
		messageType, payload, err := connection.ReadMessage()
		if err != nil {
			return
		}
		if messageType != websocket.TextMessage {
			if !s.sendWSError(client, "unsupported_frame", "realtime messages must be JSON text frames", "") {
				return
			}
			continue
		}
		var message wsClientMessage
		if err := json.Unmarshal(payload, &message); err != nil {
			if !s.sendWSError(client, "invalid_json", "message is not valid JSON", "") {
				return
			}
			continue
		}
		if !s.handleBoardWSMessage(client, board, sessionHash, message) {
			return
		}
	}
}

func (s *Server) monitorBoardWSAuthorization(client *realtime.Client, board domain.Board, sessionHash string, stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(s.config.WSAuthInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-client.Done:
			return
		case <-ticker.C:
			currentBoard, boardErr := s.repo.GetBoard(board.ID)
			if errors.Is(boardErr, store.ErrNotFound) || (boardErr == nil && currentBoard.ProjectID != board.ProjectID) {
				s.hub.Disconnect(client, realtime.CloseReasonBoardDeleted)
				return
			}
			if boardErr != nil {
				s.sendWSError(client, "authorization_unavailable", "could not verify collaboration access", "")
				s.hub.Leave(client)
				return
			}
			if _, _, _, ok, err := s.refreshBoardWSAuthorization(client, board, sessionHash); err != nil {
				s.sendWSError(client, "authorization_unavailable", "could not verify collaboration access", "")
				s.hub.Leave(client)
				return
			} else if !ok {
				s.hub.Disconnect(client, realtime.CloseReasonForbidden)
				return
			}
		}
	}
}

func (s *Server) refreshBoardWSAuthorization(client *realtime.Client, board domain.Board, sessionHash string) (domain.User, bool, bool, bool, error) {
	var user domain.User
	var canEdit, canManage, authorized bool
	var refreshErr error
	client.SynchronizeAuthorization(func() {
		user, canEdit, canManage, authorized, refreshErr = s.loadBoardWSAuthorization(client, board, sessionHash)
	})
	return user, canEdit, canManage, authorized, refreshErr
}

func (s *Server) loadBoardWSAuthorization(client *realtime.Client, board domain.Board, sessionHash string) (domain.User, bool, bool, bool, error) {
	session, err := s.repo.GetSession(sessionHash, s.config.Now().UTC())
	if errors.Is(err, store.ErrNotFound) || (err == nil && session.UserID != client.UserID) {
		return domain.User{}, false, false, false, nil
	}
	if err != nil {
		return domain.User{}, false, false, false, err
	}
	user, err := s.repo.GetUser(session.UserID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && user.MustChangePassword) {
		return domain.User{}, false, false, false, nil
	}
	if err != nil {
		return domain.User{}, false, false, false, err
	}
	canView, canEdit, canManage, err := s.projectPermissions(user, board.ProjectID)
	if err != nil {
		return domain.User{}, false, false, false, err
	}
	if !canView {
		return domain.User{}, false, false, false, nil
	}
	if !s.hub.SetPermissions(client, canEdit, canManage) {
		return domain.User{}, false, false, false, nil
	}
	return user, canEdit, canManage, true, nil
}

func (s *Server) websocketOriginAllowed(r *http.Request) bool {
	origin := strings.TrimRight(strings.TrimSpace(r.Header.Get("Origin")), "/")
	if origin == "" || sameOrigin(origin, r) {
		return true
	}
	for _, allowed := range s.config.AllowedOrigins {
		if origin == strings.TrimRight(strings.TrimSpace(allowed), "/") {
			return true
		}
	}
	return false
}

func (s *Server) writeBoardSync(connection *websocket.Conn, user domain.User, board domain.Board, client *realtime.Client) (realtime.SyncState, error) {
	canView, canEdit, canManage, err := s.projectPermissions(user, board.ProjectID)
	if err != nil {
		return realtime.SyncState{}, err
	}
	if !canView {
		return realtime.SyncState{}, store.ErrForbidden
	}
	document, updates, err := s.repo.LoadBoardDocument(board.ID)
	if err != nil {
		return realtime.SyncState{}, err
	}
	// Repositories promise sequence order, but sorting here keeps the wire
	// contract deterministic for alternate repository implementations.
	sort.Slice(updates, func(i, j int) bool {
		return updates[i].ServerSequence < updates[j].ServerSequence
	})
	client.CanEdit = canEdit
	client.CanCheckpoint = canManage
	if err := writeWSJSON(connection, realtime.Message{
		Type:      realtime.MessageSyncStart,
		Protocol:  realtime.ProtocolVersion,
		BoardID:   board.ID,
		ClientID:  client.ID,
		UserID:    user.ID,
		CanEdit:   &canEdit,
		CanManage: &canManage,
	}); err != nil {
		return realtime.SyncState{}, err
	}
	if len(document.Checkpoint) > 0 {
		if err := writeWSJSON(connection, realtime.Message{
			Type:           realtime.MessageCheckpoint,
			BoardID:        board.ID,
			ServerSequence: document.CheckpointSequence,
			Data:           document.Checkpoint,
		}); err != nil {
			return realtime.SyncState{}, err
		}
	}

	latestSequence := document.CheckpointSequence
	for _, update := range updates {
		if update.ServerSequence <= document.CheckpointSequence {
			continue
		}
		if err := writeWSJSON(connection, realtime.Message{
			Type:           realtime.MessageUpdate,
			BoardID:        board.ID,
			UpdateID:       update.UpdateID,
			ServerSequence: update.ServerSequence,
			ClientID:       update.ClientID,
			UserID:         update.UserID,
			Data:           update.Update,
		}); err != nil {
			return realtime.SyncState{}, err
		}
		if update.ServerSequence > latestSequence {
			latestSequence = update.ServerSequence
		}
	}
	if err := writeWSJSON(connection, realtime.Message{
		Type:           realtime.MessageSyncComplete,
		BoardID:        board.ID,
		ServerSequence: latestSequence,
	}); err != nil {
		return realtime.SyncState{}, err
	}
	return realtime.SyncState{
		CheckpointSequence: document.CheckpointSequence,
		LatestSequence:     latestSequence,
	}, nil
}

func (s *Server) handleBoardWSMessage(client *realtime.Client, board domain.Board, sessionHash string, message wsClientMessage) bool {
	user, canEdit, canManage, authorized, err := s.refreshBoardWSAuthorization(client, board, sessionHash)
	if err != nil {
		s.sendWSError(client, "authorization_unavailable", "could not verify collaboration access", message.UpdateID)
		return false
	}
	if !authorized {
		s.hub.Disconnect(client, realtime.CloseReasonForbidden)
		return false
	}

	switch message.Type {
	case realtime.MessageUpdate:
		if !canEdit {
			return s.sendWSError(client, "forbidden", "viewer cannot update the document", message.UpdateID)
		}
		if err := validateDocumentUpdate(message); err != nil {
			return s.sendWSError(client, "invalid_update", err.Error(), message.UpdateID)
		}
		persisted, inserted, err := s.repo.AppendBoardUpdate(domain.BoardUpdate{
			BoardID:               board.ID,
			UpdateID:              message.UpdateID,
			ClientID:              client.ID,
			UserID:                user.ID,
			Update:                message.Data,
			ReferenceBaseSequence: message.ReferenceBaseSequence,
			AssetIDs:              message.AssetIDs,
			IntroducedAssetIDs:    message.IntroducedAssetIDs,
			AssetManifestTrusted:  canManage,
		})
		if err != nil {
			if errors.Is(err, store.ErrUpdateIDConflict) {
				return s.sendWSError(client, "update_id_conflict", "update_id was already used for different data", message.UpdateID)
			}
			if errors.Is(err, store.ErrAssetClaimsRequired) {
				return s.sendWSError(client, "asset_claims_required", fmt.Sprintf("introduced_asset_ids is required by collaboration protocol v%d", realtime.ProtocolVersion), message.UpdateID)
			}
			if errors.Is(err, store.ErrInvalidAssetReference) {
				return s.sendWSError(client, "invalid_asset_reference", "asset references must exist in the board project", message.UpdateID)
			}
			return s.sendWSError(client, "persistence_failed", "could not persist document update", message.UpdateID)
		}
		acknowledged := s.hub.Send(client, realtime.Message{
			Type:           realtime.MessageUpdateAck,
			BoardID:        board.ID,
			UpdateID:       persisted.UpdateID,
			ServerSequence: persisted.ServerSequence,
			Duplicate:      !inserted,
		})
		// Publishing must not depend on the sender still being writable. A slow
		// sender can lose its ACK, but peers must still receive the committed
		// update. Re-publishing a live duplicate is safe: the hub suppresses any
		// sequence it has already published and can use it to heal a rare gap.
		if len(persisted.Update) > 0 {
			s.hub.BroadcastUpdate(board.ID, realtime.Message{
				Type:           realtime.MessageUpdate,
				BoardID:        board.ID,
				UpdateID:       persisted.UpdateID,
				ServerSequence: persisted.ServerSequence,
				ClientID:       persisted.ClientID,
				UserID:         persisted.UserID,
				Data:           persisted.Update,
			}, client)
		}
		return acknowledged

	case realtime.MessageAwareness:
		if len(message.Data) > wsMaxAwarenessBytes {
			return s.sendWSError(client, "awareness_too_large", "awareness update exceeds 64 KiB", "")
		}
		awarenessIDs, err := decodeAwarenessClientIDs(message.Data)
		if err != nil {
			return s.sendWSError(client, "invalid_awareness", "awareness update is malformed", "")
		}
		if err := s.awareness.claim(board.ID, client.ID, awarenessIDs); err != nil {
			if errors.Is(err, errAwarenessIDConflict) {
				return s.sendWSError(client, "awareness_id_conflict", "awareness ID belongs to another connection", "")
			}
			return s.sendWSError(client, "invalid_awareness", err.Error(), "")
		}
		s.hub.Broadcast(board.ID, realtime.Message{
			Type:         realtime.MessageAwareness,
			BoardID:      board.ID,
			ClientID:     client.ID,
			UserID:       user.ID,
			UserName:     awarenessDisplayName(user),
			AwarenessIDs: awarenessIDs,
			Data:         message.Data,
		}, client)
		return true

	case realtime.MessageCheckpoint:
		if !validCheckpointRequestID(message.RequestID) {
			return s.sendWSCheckpointError(client, "invalid_checkpoint", "checkpoint request_id is invalid", message.RequestID)
		}
		if !canManage {
			return s.sendWSCheckpointError(client, "forbidden", "only a project manager can save a checkpoint", message.RequestID)
		}
		if message.ThroughSequence <= 0 || len(message.Data) == 0 {
			return s.sendWSCheckpointError(client, "invalid_checkpoint", "checkpoint sequence and data are required", message.RequestID)
		}
		if len(message.Data) > wsMaxCheckpointBytes {
			return s.sendWSCheckpointError(client, "checkpoint_too_large", "checkpoint exceeds 16 MiB", message.RequestID)
		}
		if !s.hub.ValidateCheckpoint(client, message.RequestID, message.ThroughSequence) {
			return s.sendWSCheckpointError(client, "invalid_checkpoint", "checkpoint was not requested or has the wrong sequence", message.RequestID)
		}
		if _, err := s.repo.SaveBoardCheckpoint(board.ID, message.Data, message.ThroughSequence); err != nil {
			return s.sendWSCheckpointError(client, "persistence_failed", "could not persist checkpoint", message.RequestID)
		}
		completed, acknowledged := s.hub.CompleteCheckpointAndAck(client, message.RequestID, message.ThroughSequence)
		if !completed {
			return s.sendWSCheckpointError(client, "checkpoint_conflict", "checkpoint request is no longer active", message.RequestID)
		}
		return acknowledged

	default:
		return s.sendWSError(client, "unsupported_message", "unsupported realtime message type", message.UpdateID)
	}
}

func validateDocumentUpdate(message wsClientMessage) error {
	if !validUpdateID(message.UpdateID) {
		return errors.New("update_id must be between 1 and 128 non-whitespace characters")
	}
	if len(message.Data) == 0 {
		return errors.New("document update data is required")
	}
	if len(message.Data) > wsMaxUpdateBytes {
		return errors.New("document update exceeds 8 MiB")
	}
	manifestPresent := message.AssetIDs != nil
	if manifestPresent != (message.ReferenceBaseSequence != nil) {
		return errors.New("reference_base_sequence and asset_ids must be provided together")
	}
	if message.ReferenceBaseSequence != nil && *message.ReferenceBaseSequence < 0 {
		return errors.New("reference_base_sequence must be non-negative")
	}
	return nil
}

func validUpdateID(updateID string) bool {
	return updateID != "" && len(updateID) <= wsMaxUpdateIDLength && strings.TrimSpace(updateID) == updateID
}

func validCheckpointRequestID(requestID string) bool {
	if len(requestID) != len(wsCheckpointIDPrefix)+wsCheckpointIDHexLen || !strings.HasPrefix(requestID, wsCheckpointIDPrefix) {
		return false
	}
	for i := len(wsCheckpointIDPrefix); i < len(requestID); i++ {
		if value := requestID[i]; !((value >= '0' && value <= '9') || (value >= 'a' && value <= 'f')) {
			return false
		}
	}
	return true
}

func (s *Server) sendWSError(client *realtime.Client, code, message, updateID string) bool {
	if !validUpdateID(updateID) {
		updateID = ""
	}
	return s.hub.Send(client, realtime.Message{
		Type:     realtime.MessageError,
		BoardID:  client.BoardID,
		UpdateID: updateID,
		Code:     code,
		Message:  message,
	})
}

func (s *Server) sendWSCheckpointError(client *realtime.Client, code, message, requestID string) bool {
	if !validCheckpointRequestID(requestID) {
		requestID = ""
	}
	return s.hub.Send(client, realtime.Message{
		Type:      realtime.MessageError,
		BoardID:   client.BoardID,
		RequestID: requestID,
		Code:      code,
		Message:   message,
	})
}

func writeBoardWS(connection *websocket.Conn, client *realtime.Client, done chan<- struct{}) {
	ticker := time.NewTicker(wsPingPeriod)
	defer func() {
		ticker.Stop()
		_ = connection.Close()
		close(done)
	}()
	for {
		// A closed client must not drain its buffered backlog: slow-client
		// eviction is intended to release the connection promptly.
		select {
		case <-client.Done:
			writeWSClose(connection, client.CloseReason)
			return
		default:
		}
		select {
		case <-client.Done:
			writeWSClose(connection, client.CloseReason)
			return
		case payload, ok := <-client.Send:
			if !ok {
				writeWSClose(connection, client.CloseReason)
				return
			}
			if err := connection.SetWriteDeadline(time.Now().Add(wsWriteWait)); err != nil {
				return
			}
			if err := connection.WriteMessage(websocket.TextMessage, payload); err != nil {
				return
			}
		case <-ticker.C:
			if err := connection.WriteControl(websocket.PingMessage, nil, time.Now().Add(wsWriteWait)); err != nil {
				return
			}
		}
	}
}

func writeWSClose(connection *websocket.Conn, reason string) {
	_ = connection.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, reason),
		time.Now().Add(wsWriteWait),
	)
}

func writeWSJSON(connection *websocket.Conn, message realtime.Message) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	if err := connection.SetWriteDeadline(time.Now().Add(wsWriteWait)); err != nil {
		return err
	}
	return connection.WriteMessage(websocket.TextMessage, payload)
}
