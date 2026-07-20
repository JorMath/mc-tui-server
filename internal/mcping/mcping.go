// Package mcping implementa el "server list ping" de Minecraft (Java):
// handshake + status request por TCP, que devuelve un JSON con jugadores
// online, versión y MOTD. Es el mismo protocolo que usa el cliente para
// pintar la lista de servidores.
package mcping

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

// Status es la parte del JSON de status que nos interesa.
type Status struct {
	Online  int
	Max     int
	Version string
}

// writeVarInt codifica un entero en el varint del protocolo de Minecraft.
func writeVarInt(buf *bytes.Buffer, v int32) {
	u := uint32(v)
	for {
		b := byte(u & 0x7F)
		u >>= 7
		if u != 0 {
			b |= 0x80
		}
		buf.WriteByte(b)
		if u == 0 {
			return
		}
	}
}

// readVarInt decodifica un varint del reader.
func readVarInt(r io.ByteReader) (int32, error) {
	var result uint32
	for i := 0; i < 5; i++ {
		b, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		result |= uint32(b&0x7F) << (7 * i)
		if b&0x80 == 0 {
			return int32(result), nil
		}
	}
	return 0, fmt.Errorf("varint too long")
}

// packet arma un paquete: [longitud varint][id varint][payload].
func packet(id int32, payload []byte) []byte {
	var body bytes.Buffer
	writeVarInt(&body, id)
	body.Write(payload)
	var out bytes.Buffer
	writeVarInt(&out, int32(body.Len()))
	out.Write(body.Bytes())
	return out.Bytes()
}

// handshake construye el payload del handshake para status (state 1).
func handshake(host string, port uint16) []byte {
	var p bytes.Buffer
	writeVarInt(&p, -1) // versión de protocolo: -1 = "solo status"
	writeVarInt(&p, int32(len(host)))
	p.WriteString(host)
	_ = binary.Write(&p, binary.BigEndian, port)
	writeVarInt(&p, 1) // siguiente estado: status
	return p.Bytes()
}

// Ping consulta el status de un servidor en addr ("host:puerto").
func Ping(ctx context.Context, addr string) (Status, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return Status{}, fmt.Errorf("invalid address %q: %w", addr, err)
	}
	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		return Status{}, fmt.Errorf("invalid port %q: %w", portStr, err)
	}

	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return Status{}, err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	}

	if _, err := conn.Write(packet(0x00, handshake(host, uint16(port)))); err != nil {
		return Status{}, err
	}
	if _, err := conn.Write(packet(0x00, nil)); err != nil {
		return Status{}, err
	}

	r := bufio.NewReader(conn)
	if _, err := readVarInt(r); err != nil { // longitud del paquete
		return Status{}, err
	}
	if id, err := readVarInt(r); err != nil || id != 0x00 {
		return Status{}, fmt.Errorf("unexpected status packet id")
	}
	strLen, err := readVarInt(r)
	if err != nil || strLen < 0 || strLen > 1<<21 {
		return Status{}, fmt.Errorf("bad status length")
	}
	raw := make([]byte, strLen)
	if _, err := io.ReadFull(r, raw); err != nil {
		return Status{}, err
	}

	var payload struct {
		Players struct {
			Online int `json:"online"`
			Max    int `json:"max"`
		} `json:"players"`
		Version struct {
			Name string `json:"name"`
		} `json:"version"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return Status{}, fmt.Errorf("parsing status JSON: %w", err)
	}
	return Status{
		Online:  payload.Players.Online,
		Max:     payload.Players.Max,
		Version: strings.TrimSpace(payload.Version.Name),
	}, nil
}
