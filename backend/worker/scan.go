package main

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"
)

// chunkSize es el tamaño de cada tramo enviado a clamd por INSTREAM.
const chunkSize = 64 * 1024

func writeAll(conn net.Conn, b []byte) error {
	for len(b) > 0 {
		n, err := conn.Write(b)
		if err != nil {
			return err
		}
		b = b[n:]
	}
	return nil
}

// ScanFile envía un archivo local a clamd vía INSTREAM usando solo stdlib.
// Retorna infected=true con el nombre del virus si clamd reporta FOUND.
// Cualquier error de conexión/protocolo es error (el llamador reintenta → DLQ).
func ScanFile(host, filePath string) (infected bool, virus string, err error) {
	conn, err := net.DialTimeout("tcp", host, 10*time.Second)
	if err != nil {
		return false, "", err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(60 * time.Second))

	if err := writeAll(conn, []byte("zINSTREAM\x00")); err != nil {
		return false, "", err
	}
	f, err := os.Open(filePath)
	if err != nil {
		return false, "", err
	}
	defer f.Close()
	buf := make([]byte, chunkSize)
	var hdr [4]byte
	for {
		n, rerr := f.Read(buf)
		if n > 0 {
			binary.BigEndian.PutUint32(hdr[:], uint32(n))
			if err := writeAll(conn, hdr[:]); err != nil {
				return false, "", err
			}
			if err := writeAll(conn, buf[:n]); err != nil {
				return false, "", err
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return false, "", rerr
		}
	}
	if err := writeAll(conn, []byte{0, 0, 0, 0}); err != nil {
		return false, "", err
	}
	// clamd responde "stream: OK|FOUND" terminado en NUL (no en \n) y luego
	// cierra la conexión: ReadString retorna los datos junto con io.EOF.
	raw, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil && err != io.EOF {
		return false, "", err
	}
	resp := strings.TrimSpace(strings.TrimRight(raw, "\x00"))
	if resp == "" {
		return false, "", fmt.Errorf("clamd: respuesta vacía")
	}
	if strings.HasSuffix(resp, "OK") {
		return false, "", nil
	}
	if strings.HasSuffix(resp, "FOUND") {
		name := strings.TrimSuffix(strings.TrimPrefix(resp, "stream: "), " FOUND")
		return true, name, nil
	}
	return false, "", fmt.Errorf("clamd: respuesta inesperada: %s", resp)
}
