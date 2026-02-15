package antivirus

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
)

type ClamAVClient struct {
	Address string
}

func NewClamAVClient(address string) *ClamAVClient {
	return &ClamAVClient{Address: address}
}

// ScanStream sends an io.Reader stream to ClamAV over a raw TCP connection using zINSTREAM.
// It returns (isClean, virusName, error).
func (c *ClamAVClient) ScanStream(ctx context.Context, reader io.Reader) (bool, string, error) {
	if c.Address == "" {
		return true, "", nil // Bypass if not configured
	}

	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", c.Address)
	if err != nil {
		return false, "", fmt.Errorf("connect to clamav: %w", err)
	}
	defer conn.Close()

	// Handle context cancellation to close connection early
	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	// Send zINSTREAM command
	if _, err := conn.Write([]byte("zINSTREAM\000")); err != nil {
		return false, "", fmt.Errorf("write zINSTREAM command: %w", err)
	}

	// Stream chunks
	buf := make([]byte, 8192)
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			chunkSize := make([]byte, 4)
			binary.BigEndian.PutUint32(chunkSize, uint32(n))

			// Write chunk size
			if _, wErr := conn.Write(chunkSize); wErr != nil {
				return false, "", fmt.Errorf("write chunk size: %w", wErr)
			}
			// Write chunk data
			if _, wErr := conn.Write(buf[:n]); wErr != nil {
				return false, "", fmt.Errorf("write chunk data: %w", wErr)
			}
		}

		if err == io.EOF {
			break
		}
		if err != nil {
			return false, "", fmt.Errorf("read from stream: %w", err)
		}
	}

	// Write EOF chunk (length 0)
	eofChunk := []byte{0, 0, 0, 0}
	if _, err := conn.Write(eofChunk); err != nil {
		return false, "", fmt.Errorf("write EOF chunk: %w", err)
	}

	// Read response
	respBuf, err := io.ReadAll(conn)
	if err != nil && !errors.Is(err, io.EOF) {
		return false, "", fmt.Errorf("read response: %w", err)
	}

	response := strings.TrimRight(string(respBuf), "\000")
	response = strings.TrimSpace(response)

	if strings.Contains(response, "OK") {
		return true, "", nil
	}

	if strings.Contains(response, "FOUND") {
		// Example response: stream: Eicar-Test-Signature FOUND
		parts := strings.Split(response, ":")
		if len(parts) >= 2 {
			virusPart := strings.TrimSpace(parts[1])
			virusName := strings.TrimSuffix(virusPart, " FOUND")
			virusName = strings.TrimSpace(virusName)
			return false, virusName, nil
		}
		return false, "Unknown Virus", nil
	}

	return false, "", fmt.Errorf("unexpected clamav response: %s", response)
}
