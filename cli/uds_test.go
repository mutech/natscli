// Copyright 2020-2025 The NATS Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build linux

package cli

import (
	"net"
	"path/filepath"
	"testing"
)

func TestParseUDSURL(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		wantPath string
		wantUDS  bool
		wantErr  bool
	}{
		{name: "absolute path", in: "nats+uds:///run/nats/nats.sock", wantPath: "/run/nats/nats.sock", wantUDS: true},
		{name: "absolute path with whitespace", in: "  nats+uds:///tmp/x.sock  ", wantPath: "/tmp/x.sock", wantUDS: true},
		{name: "tcp url passes through", in: "nats://localhost:4222", wantUDS: false},
		{name: "tls url passes through", in: "tls://localhost:4222", wantUDS: false},
		{name: "non-empty authority rejected", in: "nats+uds://host/run/x.sock", wantUDS: true, wantErr: true},
		{name: "relative form reserved", in: "nats+uds:run/x.sock", wantUDS: true, wantErr: true},
		{name: "missing path", in: "nats+uds://", wantUDS: true, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path, isUDS, err := parseUDSURL(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tc.wantErr)
			}
			if isUDS != tc.wantUDS {
				t.Fatalf("isUDS = %v, want %v", isUDS, tc.wantUDS)
			}
			if !tc.wantErr && path != tc.wantPath {
				t.Fatalf("path = %q, want %q", path, tc.wantPath)
			}
		})
	}
}

func TestUDSConnectOptions(t *testing.T) {
	t.Run("non-uds returns no options", func(t *testing.T) {
		got, err := udsConnectOptions("nats://localhost:4222", 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != nil {
			t.Fatalf("expected no options for tcp url, got %d", len(got))
		}
	})

	t.Run("uds returns dialer and skip-host-lookup options", func(t *testing.T) {
		got, err := udsConnectOptions("nats+uds:///tmp/x.sock", 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("expected 2 options, got %d", len(got))
		}
	})

	t.Run("uds mixed with tcp is rejected", func(t *testing.T) {
		_, err := udsConnectOptions("nats+uds:///tmp/x.sock,nats://localhost:4222", 0)
		if err == nil {
			t.Fatal("expected error mixing uds with tcp, got nil")
		}
	})

	t.Run("multiple uds is rejected", func(t *testing.T) {
		_, err := udsConnectOptions("nats+uds:///tmp/a.sock,nats+uds:///tmp/b.sock", 0)
		if err == nil {
			t.Fatal("expected error for multiple uds endpoints, got nil")
		}
	})
}

// TestUDSDialerConnects proves the dialer reaches a real unix socket while
// ignoring the network/address arguments nats.go would pass it.
func TestUDSDialerConnects(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "test.sock")

	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer l.Close()

	accepted := make(chan struct{}, 1)
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		accepted <- struct{}{}
		c.Close()
	}()

	d := &udsDialer{path: sock}
	// Pass deliberately bogus tcp args to confirm they are ignored.
	conn, err := d.Dial("tcp", "203.0.113.1:4222")
	if err != nil {
		t.Fatalf("dial unix: %v", err)
	}
	defer conn.Close()

	if _, ok := conn.(*net.UnixConn); !ok {
		t.Fatalf("expected *net.UnixConn, got %T", conn)
	}
	<-accepted
}
