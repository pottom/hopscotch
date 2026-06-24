// Package keychain provides secure credential storage via the OS keychain.
// On macOS this uses Keychain Services, on Linux the Secret Service API
// (GNOME Keyring / KWallet), on Windows the Credential Manager.
package keychain

import (
	"errors"
	"fmt"

	"github.com/zalando/go-keyring"
)

const service = "hopscotch"

// ErrNotFound is returned when no credential exists for the given key.
var ErrNotFound = errors.New("not found in keychain")

// GetVPNPassword retrieves the stored password for the named VPN.
// Returns ErrNotFound if no password has been stored yet.
func GetVPNPassword(name string) (string, error) {
	pw, err := keyring.Get(service, vpnKey(name))
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("keychain get: %w", err)
	}
	return pw, nil
}

// SetVPNPassword stores or updates the password for the named VPN.
func SetVPNPassword(name, password string) error {
	if err := keyring.Set(service, vpnKey(name), password); err != nil {
		return fmt.Errorf("keychain set: %w", err)
	}
	return nil
}

// DeleteVPNPassword removes the stored password for the named VPN.
func DeleteVPNPassword(name string) error {
	if err := keyring.Delete(service, vpnKey(name)); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("keychain delete: %w", err)
	}
	return nil
}

func vpnKey(name string) string { return "vpn:" + name }
