package clouddrive

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const serviceName = "clouddrive.CloudDriveFileSrv"

const unaryTimeout = 30 * time.Second

// Push and metadata messages are small. Refuse an invalid length before allocating
// memory so a broken or hostile endpoint cannot exhaust the daemon.
const maxFrameSize = 16 << 20

type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

type PushMessageInfo struct {
	Type         int
	FileChange   bool
	PayloadBytes int
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func (c *Client) call(ctx context.Context, method string, payload []byte) ([][]byte, error) {
	return c.callWith(ctx, method, payload, false, nil)
}

func (c *Client) callWith(ctx context.Context, method string, payload []byte, stream bool, onFrame func([]byte) error) ([][]byte, error) {
	base := strings.TrimRight(c.BaseURL, "/")
	if base == "" {
		return nil, fmt.Errorf("CloudDrive2 URL is empty")
	}
	body := make([]byte, 5+len(payload))
	binary.BigEndian.PutUint32(body[1:5], uint32(len(payload)))
	copy(body[5:], payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/"+serviceName+"/"+method, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/grpc-web+proto")
	req.Header.Set("x-grpc-web", "1")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if stream {
		req.Header.Set("Accept", "application/grpc-web+proto")
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("CloudDrive2 %s: HTTP %s", method, resp.Status)
	}
	// CloudDrive2 may report gRPC failures in response headers without sending
	// a trailer frame (PushMessage permission failures do this). Do not treat
	// an empty body/EOF as a healthy stream in that case.
	if status := resp.Header.Get("grpc-status"); status != "" && status != "0" {
		message := resp.Header.Get("grpc-message")
		if decoded, decodeErr := url.QueryUnescape(message); decodeErr == nil && decoded != "" {
			message = decoded
		}
		if message == "" {
			message = "unknown error"
		}
		return nil, fmt.Errorf("CloudDrive2 %s failed: grpc-status=%s grpc-message=%s", method, status, message)
	}
	reader := bufio.NewReader(resp.Body)
	var replies [][]byte
	for {
		header := make([]byte, 5)
		if _, err := io.ReadFull(reader, header); err == io.EOF {
			break
		}
		if err != nil {
			return replies, fmt.Errorf("CloudDrive2 %s frame header: %w", method, err)
		}
		length := binary.BigEndian.Uint32(header[1:5])
		if length > maxFrameSize {
			return replies, fmt.Errorf("CloudDrive2 %s frame is too large: %d bytes", method, length)
		}
		frame := make([]byte, length)
		if _, err := io.ReadFull(reader, frame); err != nil {
			return replies, fmt.Errorf("CloudDrive2 %s frame: %w", method, err)
		}
		if header[0]&0x80 != 0 {
			status := string(frame)
			if !grpcStatusOK(status) {
				return replies, fmt.Errorf("CloudDrive2 %s failed: %s", method, status)
			}
			break
		}
		if stream && onFrame != nil {
			if err := onFrame(frame); err != nil {
				return replies, err
			}
		} else {
			replies = append(replies, frame)
		}
	}
	return replies, nil
}

func grpcStatusOK(trailer string) bool {
	for _, line := range strings.Split(strings.ReplaceAll(trailer, "\r", ""), "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 && strings.EqualFold(strings.TrimSpace(parts[0]), "grpc-status") {
			return strings.TrimSpace(parts[1]) == "0"
		}
	}
	return false
}

func (c *Client) GetMountPoints(ctx context.Context) ([]Mount, error) {
	callCtx, cancel := context.WithTimeout(ctx, unaryTimeout)
	defer cancel()
	replies, err := c.call(callCtx, "GetMountPoints", emptyRequest())
	if err != nil {
		return nil, err
	}
	if len(replies) == 0 {
		return nil, fmt.Errorf("CloudDrive2 returned no mount points")
	}
	return parseMounts(replies[0])
}

func (c *Client) ForceRefresh(ctx context.Context, path string) error {
	callCtx, cancel := context.WithTimeout(ctx, unaryTimeout)
	defer cancel()
	_, err := c.callWith(callCtx, "GetSubFiles", listSubFilesRequest(path, true), true, func([]byte) error { return nil })
	return err
}

func (c *Client) Subscribe(ctx context.Context, onChange func(Change) error) error {
	return c.SubscribeWithInfo(ctx, nil, onChange)
}

// SubscribeWithInfo keeps the PushMessage stream open and reports every
// decoded push frame. The info callback is intentionally metadata-only: it
// never exposes the bearer token or raw protobuf payload.
func (c *Client) SubscribeWithInfo(ctx context.Context, onInfo func(PushMessageInfo), onChange func(Change) error) error {
	_, err := c.callWith(ctx, "PushMessage", emptyRequest(), true, func(frame []byte) error {
		change, ok, messageType, err := parsePushMessage(frame)
		if onInfo != nil {
			onInfo(PushMessageInfo{Type: messageType, FileChange: ok, PayloadBytes: len(frame)})
		}
		if err != nil || !ok || onChange == nil {
			return err
		}
		return onChange(change)
	})
	return err
}
