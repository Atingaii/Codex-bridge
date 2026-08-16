package hub

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strings"

	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/store"
	"golang.org/x/crypto/hkdf"
)

const cliConfigVaultContext = "proofbridge-cli-config-user-vault-v1"

func (s *Server) captureCLIConfigVaultSecret(ctx context.Context, agentID, cli string, secret protocol.EncryptedSecret) (string, error) {
	private, err := s.cliConfigVaultPrivateKey()
	if err != nil {
		return "", err
	}
	result, err := s.sendCLIConfigRequest(ctx, agentID, protocol.TypeCLIConfigExport, protocol.CLIConfigRequest{
		CLI: cli, Secret: secret, RecipientPublicKey: base64.RawStdEncoding.EncodeToString(private.PublicKey().Bytes()),
	})
	if err != nil {
		return "", err
	}
	if !result.OK || !encryptedSecretPresent(result.Secret) {
		if strings.TrimSpace(result.Error) != "" {
			return "", errors.New(result.Error)
		}
		return "", errors.New("Bridge did not export the encrypted API key")
	}
	plaintext, err := decryptCLIConfigEnvelope(private, result.Secret)
	if err != nil {
		return "", err
	}
	defer clear(plaintext)
	return s.sealCLIConfigVaultSecret(plaintext)
}

func (s *Server) materializeCLIConfigPresetCredential(ctx context.Context, userID, agentID string, preset *store.CLIConfigPreset) error {
	if preset == nil {
		return store.ErrNotFound
	}
	connection, online := s.pool.AgentConnectionInfo(agentID)
	if !online || connection.Capabilities == nil || connection.Capabilities.ConfigSwitcher == nil {
		return ErrAgentOffline
	}
	capability := connection.Capabilities.ConfigSwitcher
	if preset.CredentialAvailable && preset.KeyHint == capability.KeyID && encryptedSecretPresent(preset.Secret) {
		if preset.VaultSecret == "" && capability.Version >= 2 {
			vaultSecret, err := s.captureCLIConfigVaultSecret(ctx, agentID, preset.CLI, preset.Secret)
			if err != nil {
				return err
			}
			if err := s.store.PutCLIConfigPresetVaultSecret(ctx, preset.ID, userID, vaultSecret); err != nil {
				return err
			}
			preset.VaultSecret = vaultSecret
		}
		return nil
	}
	if preset.VaultSecret == "" {
		credentials, err := s.store.ListCLIConfigPresetCredentials(ctx, preset.ID, userID)
		if err != nil {
			return err
		}
		for _, source := range credentials {
			sourceConnection, sourceOnline := s.pool.AgentConnectionInfo(source.AgentID)
			if !sourceOnline || sourceConnection.Capabilities == nil || sourceConnection.Capabilities.ConfigSwitcher == nil || sourceConnection.Capabilities.ConfigSwitcher.Version < 2 || source.KeyHint != sourceConnection.Capabilities.ConfigSwitcher.KeyID {
				continue
			}
			vaultSecret, err := s.captureCLIConfigVaultSecret(ctx, source.AgentID, source.CLI, source.Secret)
			if err != nil {
				continue
			}
			if err := s.store.PutCLIConfigPresetVaultSecret(ctx, preset.ID, userID, vaultSecret); err != nil {
				return err
			}
			preset.VaultSecret = vaultSecret
			break
		}
	}
	if preset.VaultSecret == "" {
		return errors.New("saved API key is not yet available in the user model library; bring its original upgraded Bridge online or enter the API key once")
	}
	plaintext, err := s.openCLIConfigVaultSecret(preset.VaultSecret)
	if err != nil {
		return err
	}
	defer clear(plaintext)
	secret, err := encryptCLIConfigForPublicKey(capability.PublicKey, plaintext)
	if err != nil {
		return err
	}
	if err := s.store.PutCLIConfigPresetCredential(ctx, preset.ID, userID, agentID, capability.KeyID, secret); err != nil {
		return err
	}
	preset.AgentID = agentID
	preset.KeyHint = capability.KeyID
	preset.Secret = secret
	preset.CredentialAvailable = true
	return nil
}

func (s *Server) cliConfigVaultPrivateKey() (*ecdh.PrivateKey, error) {
	digest := sha256.Sum256([]byte(cliConfigVaultContext + "\x00" + s.cfg.Auth.JWTSecret))
	order := new(big.Int).Sub(elliptic.P256().Params().N, big.NewInt(1))
	scalar := new(big.Int).SetBytes(digest[:])
	scalar.Mod(scalar, order)
	scalar.Add(scalar, big.NewInt(1))
	bytes := scalar.FillBytes(make([]byte, 32))
	defer clear(bytes)
	return ecdh.P256().NewPrivateKey(bytes)
}

func (s *Server) vaultKey() [32]byte {
	return sha256.Sum256([]byte(cliConfigVaultContext + "\x01" + s.cfg.Auth.JWTSecret))
}

func (s *Server) sealCLIConfigVaultSecret(plaintext []byte) (string, error) {
	key := s.vaultKey()
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := aead.Seal(nil, nonce, plaintext, []byte(cliConfigVaultContext))
	return base64.RawStdEncoding.EncodeToString(append(nonce, sealed...)), nil
}

func (s *Server) openCLIConfigVaultSecret(value string) ([]byte, error) {
	raw, err := base64.RawStdEncoding.DecodeString(value)
	if err != nil {
		return nil, err
	}
	key := s.vaultKey()
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil || len(raw) < aead.NonceSize() {
		return nil, errors.New("invalid user model credential")
	}
	return aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], []byte(cliConfigVaultContext))
}

func decryptCLIConfigEnvelope(private *ecdh.PrivateKey, secret protocol.EncryptedSecret) ([]byte, error) {
	decode := func(value string) ([]byte, error) { return base64.RawStdEncoding.DecodeString(value) }
	publicRaw, err := decode(secret.EphemeralPublicKey)
	if err != nil {
		return nil, err
	}
	public, err := ecdh.P256().NewPublicKey(publicRaw)
	if err != nil {
		return nil, err
	}
	shared, err := private.ECDH(public)
	if err != nil {
		return nil, err
	}
	salt, err := decode(secret.Salt)
	if err != nil {
		return nil, err
	}
	iv, err := decode(secret.IV)
	if err != nil {
		return nil, err
	}
	ciphertext, err := decode(secret.Ciphertext)
	if err != nil {
		return nil, err
	}
	derived, err := deriveCLIConfigKey(shared, salt)
	if err != nil {
		return nil, err
	}
	defer clear(derived)
	block, err := aes.NewCipher(derived)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return aead.Open(nil, iv, ciphertext, nil)
}

func encryptCLIConfigForPublicKey(publicKey string, plaintext []byte) (protocol.EncryptedSecret, error) {
	publicRaw, err := base64.RawStdEncoding.DecodeString(publicKey)
	if err != nil {
		return protocol.EncryptedSecret{}, err
	}
	public, err := ecdh.P256().NewPublicKey(publicRaw)
	if err != nil {
		return protocol.EncryptedSecret{}, err
	}
	ephemeral, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return protocol.EncryptedSecret{}, err
	}
	shared, err := ephemeral.ECDH(public)
	if err != nil {
		return protocol.EncryptedSecret{}, err
	}
	salt := make([]byte, 16)
	iv := make([]byte, 12)
	if _, err := rand.Read(salt); err != nil {
		return protocol.EncryptedSecret{}, err
	}
	if _, err := rand.Read(iv); err != nil {
		return protocol.EncryptedSecret{}, err
	}
	derived, err := deriveCLIConfigKey(shared, salt)
	if err != nil {
		return protocol.EncryptedSecret{}, err
	}
	defer clear(derived)
	block, err := aes.NewCipher(derived)
	if err != nil {
		return protocol.EncryptedSecret{}, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return protocol.EncryptedSecret{}, err
	}
	return protocol.EncryptedSecret{
		EphemeralPublicKey: base64.RawStdEncoding.EncodeToString(ephemeral.PublicKey().Bytes()),
		Salt:               base64.RawStdEncoding.EncodeToString(salt), IV: base64.RawStdEncoding.EncodeToString(iv),
		Ciphertext: base64.RawStdEncoding.EncodeToString(aead.Seal(nil, iv, plaintext, nil)),
	}, nil
}

func deriveCLIConfigKey(shared, salt []byte) ([]byte, error) {
	reader := hkdf.New(sha256.New, shared, salt, []byte("codex-bridge-cli-config-v1"))
	key := make([]byte, 32)
	if _, err := io.ReadFull(reader, key); err != nil {
		return nil, fmt.Errorf("derive CLI config key: %w", err)
	}
	return key, nil
}
