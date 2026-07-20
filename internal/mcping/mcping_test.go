package mcping

import (
	"bufio"
	"bytes"
	"context"
	"net"
	"testing"
	"time"
)

// fakeServer implementa el lado servidor del protocolo de status y
// devuelve el JSON dado.
func fakeServer(t *testing.T, statusJSON string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		// handshake: [len][payload...]
		n, _ := readVarInt(r)
		if _, err := r.Discard(int(n)); err != nil {
			return
		}
		// status request
		n, _ = readVarInt(r)
		if _, err := r.Discard(int(n)); err != nil {
			return
		}
		// respuesta: [len][0x00][strlen][json]
		var payload bytes.Buffer
		writeVarInt(&payload, int32(len(statusJSON)))
		payload.WriteString(statusJSON)
		conn.Write(packet(0x00, payload.Bytes()))
	}()
	return ln.Addr().String()
}

func TestPingParsesStatus(t *testing.T) {
	addr := fakeServer(t, `{"players":{"online":3,"max":20},"version":{"name":"Paper 1.21.4"},"description":{"text":"hola"}}`)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	st, err := Ping(ctx, addr)
	if err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if st.Online != 3 || st.Max != 20 || st.Version != "Paper 1.21.4" {
		t.Fatalf("status = %+v", st)
	}
}

func TestPingConnectionRefused(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := Ping(ctx, "127.0.0.1:1"); err == nil {
		t.Fatal("puerto cerrado debe fallar")
	}
}

func TestPingBadAddress(t *testing.T) {
	if _, err := Ping(context.Background(), "sin-puerto"); err == nil {
		t.Fatal("dirección inválida debe fallar")
	}
}

func TestVarIntRoundTrip(t *testing.T) {
	for _, v := range []int32{0, 1, 127, 128, 300, 25565, -1} {
		var buf bytes.Buffer
		writeVarInt(&buf, v)
		got, err := readVarInt(&buf)
		if err != nil {
			t.Fatalf("readVarInt(%d): %v", v, err)
		}
		if got != v {
			t.Fatalf("roundtrip %d = %d", v, got)
		}
	}
}

func TestPingGarbageJSONFails(t *testing.T) {
	addr := fakeServer(t, `{esto no es json`)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := Ping(ctx, addr); err == nil {
		t.Fatal("JSON inválido debe fallar")
	}
}
