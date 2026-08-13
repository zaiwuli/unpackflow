package clouddrive

import (
	"context"
	"encoding/binary"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func frame(flags byte, payload []byte) []byte {
	out := make([]byte, 5+len(payload))
	out[0] = flags
	binary.BigEndian.PutUint32(out[1:5], uint32(len(payload)))
	copy(out[5:], payload)
	return out
}

func TestGetMountPointsUsesBearerAndParsesGrpcWeb(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/clouddrive.CloudDriveFileSrv/GetMountPoints" {
			t.Errorf("path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("missing bearer token")
		}
		_, _ = io.Copy(io.Discard, r.Body)
		var mount []byte
		putString(&mount, 1, "/mnt/cd2")
		putString(&mount, 2, "/115open")
		putBool(&mount, 9, true)
		var reply []byte
		putBytes(&reply, 1, mount)
		_, _ = w.Write(append(frame(0, reply), frame(0x80, []byte("grpc-status: 0\r\n"))...))
	}))
	defer server.Close()
	mounts, err := (&Client{BaseURL: server.URL, Token: "test-token"}).GetMountPoints(context.Background())
	if err != nil || len(mounts) != 1 || mounts[0].SourceDir != "/115open" {
		t.Fatalf("mounts=%#v err=%v", mounts, err)
	}
}

func TestForceRefreshConsumesStreamingFrames(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = w.Write(append(frame(0, []byte{1, 2, 3}), frame(0x80, []byte("grpc-status: 0\r\n"))...))
	}))
	defer server.Close()
	if err := (&Client{BaseURL: server.URL}).ForceRefresh(context.Background(), "/115open"); err != nil {
		t.Fatal(err)
	}
}

func TestForceRefreshAcceptsGrpcStatusWithoutSpace(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = w.Write(frame(0x80, []byte("grpc-status:0\r\n")))
	}))
	defer server.Close()
	if err := (&Client{BaseURL: server.URL}).ForceRefresh(context.Background(), "/115open"); err != nil {
		t.Fatal(err)
	}
}

func TestClientRejectsOversizedFrame(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		header := []byte{0, 1, 0, 0, 1} // 16 MiB + 1 byte, larger than the allowed maximum.
		_, _ = w.Write(header)
	}))
	defer server.Close()
	if _, err := (&Client{BaseURL: server.URL}).GetMountPoints(context.Background()); err == nil {
		t.Fatal("expected oversized frame error")
	}
}
