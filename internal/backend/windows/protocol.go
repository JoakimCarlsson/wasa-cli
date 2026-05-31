//go:build windows

package conpty

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

// The daemon and its clients speak a tiny framed protocol over the per-user
// named pipe: a 4-byte little-endian length prefix followed by that many bytes
// of JSON. Each control operation is one request frame and one response frame.
//
// Attach is the exception: after the client sends an attach request and reads
// one response frame acknowledging it, the connection drops out of framing and
// becomes a raw, bidirectional byte relay between the terminal and the session's
// pseudo-console until the client disconnects.

type opCode string

const (
	opSpawn   opCode = "spawn"
	opAttach  opCode = "attach"
	opCapture opCode = "capture"
	opHas     opCode = "has"
	opList    opCode = "list"
	opKill    opCode = "kill"
	opPing    opCode = "ping"
)

// request is a single operation sent to the daemon. Only the fields relevant to
// Op are populated.
type request struct {
	Op      opCode   `json:"op"`
	Name    string   `json:"name,omitempty"`
	Dir     string   `json:"dir,omitempty"`
	Env     []string `json:"env,omitempty"`
	Program []string `json:"program,omitempty"`
	Cols    int16    `json:"cols,omitempty"`
	Rows    int16    `json:"rows,omitempty"`
}

// response is the daemon's reply. Err non-empty means the operation failed.
type response struct {
	Err     string   `json:"err,omitempty"`
	Exists  bool     `json:"exists,omitempty"`
	Names   []string `json:"names,omitempty"`
	Capture string   `json:"capture,omitempty"`
}

// maxFrame caps a single JSON frame so a corrupt length prefix cannot make the
// reader allocate without bound. Control frames are tiny; captures are one
// screen of text.
const maxFrame = 4 << 20

func writeFrame(w io.Writer, v any) error {
	payload, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(payload) > maxFrame {
		return fmt.Errorf("conpty: frame too large (%d bytes)", len(payload))
	}
	var header [4]byte
	binary.LittleEndian.PutUint32(header[:], uint32(len(payload)))
	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	_, err = w.Write(payload)
	return err
}

func readFrame(r io.Reader, v any) error {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return err
	}
	n := binary.LittleEndian.Uint32(header[:])
	if n > maxFrame {
		return fmt.Errorf("conpty: frame too large (%d bytes)", n)
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return err
	}
	return json.Unmarshal(payload, v)
}
