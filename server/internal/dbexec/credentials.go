package dbexec

import (
	"github.com/chy/chat2db/server/internal/config"
	cryptopkg "github.com/chy/chat2db/server/internal/crypto"
	"github.com/chy/chat2db/server/internal/model"
)

// decryptConnPassword decrypts the stored credential password for the
// given connection using the process-wide CredentialKey.
//
// This helper was historically owned by the service package, but dbexec
// is the only real caller. Keeping it here removes a dbexec→service import
// edge that otherwise conflicts with service wanting to consume dbexec
// capabilities (e.g. Capabilities.DefaultPort).
func decryptConnPassword(c *model.Connection) (string, error) {
	return cryptopkg.DecryptString(c.PasswordEnc, config.Get().CredentialKey)
}
