package main

import (
	"encoding/binary"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeClamd atiende una conexión INSTREAM: acumula los bytes y responde
// FOUND si contienen la firma EICAR, OK en caso contrario.
func fakeClamd(t *testing.T, ln net.Listener) {
	t.Helper()
	conn, err := ln.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	cmd := make([]byte, 10)
	if _, err := io.ReadFull(conn, cmd); err != nil {
		t.Errorf("cmd: %v", err)
		return
	}
	var data []byte
	var hdr [4]byte
	for {
		if _, err := io.ReadFull(conn, hdr[:]); err != nil {
			t.Errorf("hdr: %v", err)
			return
		}
		n := binary.BigEndian.Uint32(hdr[:])
		if n == 0 {
			break
		}
		chunk := make([]byte, n)
		if _, err := io.ReadFull(conn, chunk); err != nil {
			t.Errorf("chunk: %v", err)
			return
		}
		data = append(data, chunk...)
	}
	if strings.Contains(string(data), `X5O!P%@AP[4\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*`) {
		// Como el clamd real: respuesta terminada en NUL y cierre.
		_, _ = conn.Write([]byte("stream: Eicar-Test-Signature FOUND\x00"))
		return
	}
	_, _ = conn.Write([]byte("stream: OK\x00"))
}

func scanWithFake(t *testing.T, content string) (bool, string, error) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	done := make(chan struct{})
	go func() { defer close(done); fakeClamd(t, ln) }()
	path := filepath.Join(t.TempDir(), "sample.bin")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	infected, virus, err := ScanFile(ln.Addr().String(), path)
	<-done
	return infected, virus, err
}

func TestScanFileClean(t *testing.T) {
	infected, virus, err := scanWithFake(t, "contenido multimedia legítimo")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if infected || virus != "" {
		t.Fatalf("falso positivo: infected=%v virus=%q", infected, virus)
	}
}

func TestScanFileInfected(t *testing.T) {
	eicar := `X5O!P%@AP[4\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*`
	infected, virus, err := scanWithFake(t, "prefijo "+eicar+" sufijo")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !infected || virus != "Eicar-Test-Signature" {
		t.Fatalf("no detectado: infected=%v virus=%q", infected, virus)
	}
}

func TestScanFileUnreachable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.bin")
	_ = os.WriteFile(path, []byte("x"), 0600)
	// Puerto cerrado: debe ser error (el worker reintenta → DLQ, fail-closed).
	if _, _, err := ScanFile("127.0.0.1:1", path); err == nil {
		t.Fatal("se esperaba error de conexión")
	}
}
