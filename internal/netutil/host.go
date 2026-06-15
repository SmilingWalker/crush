// Package netutil holds URL/host-parsing helpers shared across the server and
// client packages. It exists as a leaf package (no internal/ imports) so that
// internal/client can use these helpers without importing internal/server —
// which previously created a client→server import edge that, combined with
// workspace→client, made the team-data-domain TeamWorkspace (in package
// workspace) unreachable from the server-side DAG. Moving the helpers here
// breaks that latent cycle (M3-07).
package netutil

import (
	"fmt"
	"net/url"
	"os/user"
	"runtime"
	"strings"
)

// ParseHostURL parses a host URL into a [url.URL]. Accepts tcp://, unix://, and
// npipe:// schemes; for tcp the host:port (and any path) are split out.
func ParseHostURL(host string) (*url.URL, error) {
	proto, addr, ok := strings.Cut(host, "://")
	if !ok {
		return nil, fmt.Errorf("invalid host format: %s", host)
	}

	var basePath string
	if proto == "tcp" {
		parsed, err := url.Parse("tcp://" + addr)
		if err != nil {
			return nil, fmt.Errorf("invalid tcp address: %v", err)
		}
		addr = parsed.Host
		basePath = parsed.Path
	}
	return &url.URL{
		Scheme: proto,
		Host:   addr,
		Path:   basePath,
	}, nil
}

// DefaultHost returns the default server host: a per-user named pipe on Windows
// and a per-user unix socket elsewhere.
func DefaultHost() string {
	sock := "crush.sock"
	usr, err := user.Current()
	if err == nil && usr.Uid != "" {
		sock = fmt.Sprintf("crush-%s.sock", usr.Uid)
	}
	if runtime.GOOS == "windows" {
		return fmt.Sprintf("npipe:////./pipe/%s", sock)
	}
	return fmt.Sprintf("unix:///tmp/%s", sock)
}
