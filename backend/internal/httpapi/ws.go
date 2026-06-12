package httpapi

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"dreamwhiteboard/backend/internal/domain"
	"dreamwhiteboard/backend/internal/realtime"
)

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

type wsClientMessage struct {
	Type        string         `json:"type"`
	ClientID    string         `json:"client_id"`
	OperationID string         `json:"op_id"`
	Operation   string         `json:"operation"`
	BaseVersion int64          `json:"base_version"`
	Payload     map[string]any `json:"payload"`
}

func (s *Server) handleBoardWS(w http.ResponseWriter, r *http.Request, user domain.User, board domain.Board) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		writeError(w, http.StatusBadRequest, "websocket upgrade required")
		return
	}
	conn, rw, err := hijackWebSocket(w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	defer conn.Close()

	client := &realtime.Client{
		ID:      r.URL.Query().Get("client_id"),
		UserID:  user.ID,
		BoardID: board.ID,
		Send:    make(chan []byte, 32),
	}
	if client.ID == "" {
		client.ID = randomID("cli")
	}
	s.hub.Join(client)
	defer s.hub.Leave(client)

	snapshot, _ := s.repo.Snapshot(board.ID)
	client.Send <- realtime.Encode(realtime.Message{Type: "snapshot", BoardID: board.ID, ClientID: client.ID, Snapshot: &snapshot})

	done := make(chan struct{})
	go func() {
		defer close(done)
		for payload := range client.Send {
			if err := writeWSFrame(rw.Writer, payload); err != nil {
				return
			}
		}
	}()

	for {
		payload, err := readWSFrame(rw.Reader)
		if err != nil {
			return
		}
		var msg wsClientMessage
		if err := json.Unmarshal(payload, &msg); err != nil {
			client.Send <- realtime.Encode(realtime.Message{Type: "error", Error: "invalid json"})
			continue
		}
		switch msg.Type {
		case "join":
			snapshot, _ := s.repo.Snapshot(board.ID)
			client.Send <- realtime.Encode(realtime.Message{Type: "snapshot", BoardID: board.ID, ClientID: client.ID, Snapshot: &snapshot})
		case "cursor", "presence":
			s.hub.Broadcast(board.ID, realtime.Message{Type: msg.Type, BoardID: board.ID, ClientID: client.ID, UserID: user.ID, Payload: msg.Payload}, client)
		case "operation":
			if !s.canEditProject(user, board.ProjectID) {
				client.Send <- realtime.Encode(realtime.Message{Type: "error", Error: "viewer cannot mutate board"})
				continue
			}
			op := domain.Operation{
				ID:          msg.OperationID,
				BoardID:     board.ID,
				ClientID:    client.ID,
				UserID:      user.ID,
				Type:        msg.Operation,
				BaseVersion: msg.BaseVersion,
				Payload:     msg.Payload,
			}
			applied, snapshot, err := s.repo.ApplyOperation(op)
			if err != nil {
				client.Send <- realtime.Encode(realtime.Message{Type: "error", Error: err.Error()})
				continue
			}
			client.Send <- realtime.Encode(realtime.Message{Type: "operation_ack", BoardID: board.ID, ClientID: client.ID, Operation: &applied, Snapshot: &snapshot})
			s.hub.Broadcast(board.ID, realtime.Message{Type: "operation_broadcast", BoardID: board.ID, ClientID: client.ID, Operation: &applied}, client)
		default:
			client.Send <- realtime.Encode(realtime.Message{Type: "error", Error: "unsupported message type"})
		}
		select {
		case <-done:
			return
		default:
		}
	}
}

func hijackWebSocket(w http.ResponseWriter, r *http.Request) (net.Conn, *bufio.ReadWriter, error) {
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		return nil, nil, errors.New("missing Sec-WebSocket-Key")
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("hijacking unsupported")
	}
	conn, rw, err := hijacker.Hijack()
	if err != nil {
		return nil, nil, err
	}
	sum := sha1.Sum([]byte(key + wsGUID))
	accept := base64.StdEncoding.EncodeToString(sum[:])
	response := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + accept + "\r\n\r\n"
	if _, err := rw.WriteString(response); err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	if err := rw.Flush(); err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	return conn, rw, nil
}

func readWSFrame(r *bufio.Reader) ([]byte, error) {
	first, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	opcode := first & 0x0f
	if opcode == 0x8 {
		return nil, io.EOF
	}
	if opcode != 0x1 {
		return nil, fmt.Errorf("unsupported websocket opcode %d", opcode)
	}
	second, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	masked := second&0x80 != 0
	length := uint64(second & 0x7f)
	switch length {
	case 126:
		var n uint16
		if err := binary.Read(r, binary.BigEndian, &n); err != nil {
			return nil, err
		}
		length = uint64(n)
	case 127:
		if err := binary.Read(r, binary.BigEndian, &length); err != nil {
			return nil, err
		}
	}
	var mask [4]byte
	if masked {
		if _, err := io.ReadFull(r, mask[:]); err != nil {
			return nil, err
		}
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return payload, nil
}

func writeWSFrame(w *bufio.Writer, payload []byte) error {
	if err := w.WriteByte(0x81); err != nil {
		return err
	}
	switch {
	case len(payload) < 126:
		if err := w.WriteByte(byte(len(payload))); err != nil {
			return err
		}
	case len(payload) <= 65535:
		if err := w.WriteByte(126); err != nil {
			return err
		}
		if err := binary.Write(w, binary.BigEndian, uint16(len(payload))); err != nil {
			return err
		}
	default:
		if err := w.WriteByte(127); err != nil {
			return err
		}
		if err := binary.Write(w, binary.BigEndian, uint64(len(payload))); err != nil {
			return err
		}
	}
	if _, err := w.Write(payload); err != nil {
		return err
	}
	return w.Flush()
}

func init() {
	_ = time.Now
}
