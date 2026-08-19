package release

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
)

const (
	signifyPublicKeySize = 2 + 8 + ed25519.PublicKeySize
	signifySignatureSize = 2 + 8 + ed25519.SignatureSize
)

type VerifiedKey struct {
	ID         string
	FileSHA256 string
}

func VerifySignify(document, signatureFile []byte, publicKeyFiles [][]byte) (VerifiedKey, error) {
	signature, signatureKeyID, err := parseSignifyFile(signatureFile, signifySignatureSize, "signature")
	if err != nil {
		return VerifiedKey{}, err
	}
	if !bytes.Equal(signature[:2], []byte("Ed")) {
		return VerifiedKey{}, errors.New("unsupported signify signature algorithm")
	}
	var matchingKeys int
	var verified *VerifiedKey
	for _, keyFile := range publicKeyFiles {
		key, keyID, parseErr := parseSignifyFile(keyFile, signifyPublicKeySize, "public key")
		if parseErr != nil {
			return VerifiedKey{}, parseErr
		}
		if !bytes.Equal(key[:2], []byte("Ed")) {
			return VerifiedKey{}, errors.New("unsupported signify public-key algorithm")
		}
		if keyID != signatureKeyID {
			continue
		}
		matchingKeys++
		if ed25519.Verify(ed25519.PublicKey(key[10:]), document, signature[10:]) {
			digest := sha256.Sum256(keyFile)
			candidate := VerifiedKey{ID: hex.EncodeToString(key[2:10]), FileSHA256: hex.EncodeToString(digest[:])}
			verified = &candidate
		}
	}
	if matchingKeys == 0 {
		return VerifiedKey{}, fmt.Errorf("signature key ID %s is not trusted", signatureKeyID)
	}
	if matchingKeys != 1 {
		return VerifiedKey{}, fmt.Errorf("trusted keyring contains %d entries for signature key ID %s", matchingKeys, signatureKeyID)
	}
	if verified == nil {
		return VerifiedKey{}, errors.New("release-index signature verification failed")
	}
	return *verified, nil
}

func parseSignifyFile(content []byte, decodedSize int, description string) ([]byte, string, error) {
	lines := bytes.Split(content, []byte{'\n'})
	if len(lines) == 3 && len(lines[2]) == 0 {
		lines = lines[:2]
	}
	if len(lines) != 2 || !bytes.HasPrefix(lines[0], []byte("untrusted comment: ")) {
		return nil, "", fmt.Errorf("%s is not a detached signify file", description)
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(string(lines[1]))
	if err != nil || len(decoded) != decodedSize {
		return nil, "", fmt.Errorf("%s has invalid signify payload", description)
	}
	return decoded, hex.EncodeToString(decoded[2:10]), nil
}
