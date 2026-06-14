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

package cli

import (
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
)

// udsScheme is the connection URL scheme used to reach a NATS server over a
// UNIX domain socket: nats+uds:///<absolute-path>.
const udsScheme = "nats+uds"

// udsDialer implements nats.CustomDialer for UNIX domain socket connections.
//
// The socket path is captured at construction. The network and address that
// nats.go passes to Dial are ignored on purpose: nats.go always calls
// Dial("tcp", host), and for a nats+uds:/// URL the host is empty, so the
// arguments carry no useful information. Pinning the path here also guarantees
// the connection can never silently fall back to TCP (e.g. via server-advertised
// cluster URLs) — every dial attempt goes to the local socket.
type udsDialer struct {
	path    string
	timeout time.Duration
}

func (d *udsDialer) Dial(_, _ string) (net.Conn, error) {
	if d.timeout <= 0 {
		return net.Dial("unix", d.path)
	}
	return net.DialTimeout("unix", d.path, d.timeout)
}

// parseUDSURL inspects a single server URL. It returns isUDS=true when the URL
// uses the nats+uds scheme, along with the absolute socket path. Non-UDS URLs
// return isUDS=false and no error so callers can pass them through unchanged.
//
// Supported form: nats+uds:///<absolute-path> (empty authority, absolute path).
// The relative form nats+uds:<path> is reserved but not yet implemented.
func parseUDSURL(s string) (path string, isUDS bool, err error) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, udsScheme+":") {
		return "", false, nil
	}

	u, err := url.Parse(s)
	if err != nil {
		return "", true, fmt.Errorf("invalid %s URL %q: %w", udsScheme, s, err)
	}

	switch {
	case u.Host != "":
		return "", true, fmt.Errorf("invalid %s URL %q: authority must be empty, use %s:///<absolute-path>", udsScheme, s, udsScheme)
	case u.Opaque != "":
		return "", true, fmt.Errorf("relative %s paths are not supported yet, use %s:///<absolute-path>", udsScheme, udsScheme)
	case u.Path == "":
		return "", true, fmt.Errorf("invalid %s URL %q: missing socket path", udsScheme, s)
	default:
		return u.Path, true, nil
	}
}

// udsConnectOptions returns the nats.Option(s) needed to connect over a UNIX
// domain socket when serverURL designates a nats+uds endpoint, or nil when it
// does not. serverURL may be a comma-separated list as accepted by nats.Connect.
//
// A nats+uds endpoint is local-only and has no failover peers, so it must be the
// sole server in the list; mixing it with other URLs is rejected.
func udsConnectOptions(serverURL string, timeout time.Duration) ([]nats.Option, error) {
	servers := splitServerList(serverURL)

	var path string
	udsCount := 0
	for _, s := range servers {
		p, isUDS, err := parseUDSURL(s)
		if err != nil {
			return nil, err
		}
		if isUDS {
			udsCount++
			path = p
		}
	}

	if udsCount == 0 {
		return nil, nil
	}
	if len(servers) > 1 {
		return nil, fmt.Errorf("a %s endpoint must be the only configured server, got %d: %s", udsScheme, len(servers), serverURL)
	}

	return []nats.Option{
		nats.SetCustomDialer(&udsDialer{path: path, timeout: timeout}),
		// The dialer ignores the address, and nats+uds:///<path> has an empty
		// host, so skip the otherwise-pointless DNS lookup of "".
		nats.SkipHostLookup(),
	}, nil
}

// splitServerList splits a comma-separated server URL string into its individual
// entries, trimming whitespace and dropping empties.
func splitServerList(serverURL string) []string {
	var out []string
	for _, s := range strings.Split(serverURL, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
