package tunnel

import (
	"context"
	"net"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/ssh/agent"
)

// watchAgentKeys returns a channel that closes when a new key appears in the
// running SSH agent (SSH_AUTH_SOCK). Useful for retrying immediately after a
// YubiKey or new identity is added during a backoff wait.
// Returns a channel that never closes if no agent is available.
func watchAgentKeys(ctx context.Context) <-chan struct{} {
	ch := make(chan struct{})
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return ch
	}

	before := agentKeySet(sock)

	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				current := agentKeySet(sock)
				for blob := range current {
					if !before[blob] {
						close(ch)
						return
					}
				}
			}
		}
	}()
	return ch
}

// agentKeySet returns the set of key blobs currently held by the SSH agent.
func agentKeySet(sock string) map[string]bool {
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return nil
	}
	defer conn.Close()

	keys, err := agent.NewClient(conn).List()
	if err != nil {
		return nil
	}

	set := make(map[string]bool, len(keys))
	for _, k := range keys {
		set[string(k.Blob)] = true
	}
	return set
}

// isAuthError reports whether the tunnel error string looks like an SSH
// authentication failure (as opposed to a network/connection error). Auth
// errors won't self-heal without a key change, so we watch the agent.
func isAuthError(errMsg string) bool {
	return strings.Contains(errMsg, "unable to authenticate") ||
		strings.Contains(errMsg, "no supported methods remain") ||
		strings.Contains(errMsg, "handshake failed")
}
