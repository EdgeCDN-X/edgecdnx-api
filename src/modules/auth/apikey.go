package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
)

const (
	APIKeyFormatVersion    = "v1"
	APIKeyDigestAlgorithm  = "sha256"
	APIKeySubjectPrefix    = "ak_"
	APIKeyObjectNamePrefix = "ak-"

	apiKeyIDLength     = 16
	apiKeyIDAlphabet   = "abcdefghijklmnopqrstuvwxyz0123456789"
	apiKeySecretBytes  = 32
	apiKeyDigestDomain = "edgecdnx-api-key:v1:"
)

// APIKeyCredential holds a newly generated API key. Token is the only copy of the
// usable credential and must never be persisted.
type APIKeyCredential struct {
	ID     string
	Token  string
	Digest string
}

func NewAPIKeyCredential() (APIKeyCredential, error) {
	id, err := randomAPIKeyID()
	if err != nil {
		return APIKeyCredential{}, err
	}

	secret := make([]byte, apiKeySecretBytes)
	if _, err := rand.Read(secret); err != nil {
		return APIKeyCredential{}, fmt.Errorf("generate API key secret: %w", err)
	}

	token := APIKeySubjectPrefix + id + "_" + base64.RawURLEncoding.EncodeToString(secret)
	return APIKeyCredential{
		ID:     id,
		Token:  token,
		Digest: APIKeyDigest(token),
	}, nil
}

func APIKeyDigest(token string) string {
	sum := sha256.Sum256([]byte(apiKeyDigestDomain + token))
	return hex.EncodeToString(sum[:])
}

func APIKeyObjectName(id string) string {
	return APIKeyObjectNamePrefix + id
}

func APIKeySubject(id string) string {
	return APIKeySubjectPrefix + id
}

// APIKeyIDFromObjectName returns the random ID from an APIKey CR name.
func APIKeyIDFromObjectName(name string) (string, bool) {
	id, ok := strings.CutPrefix(name, APIKeyObjectNamePrefix)
	if !ok || !isValidAPIKeyID(id) {
		return "", false
	}
	return id, true
}

func randomAPIKeyID() (string, error) {
	alphabetSize := big.NewInt(int64(len(apiKeyIDAlphabet)))
	id := make([]byte, apiKeyIDLength)
	for i := range id {
		n, err := rand.Int(rand.Reader, alphabetSize)
		if err != nil {
			return "", fmt.Errorf("generate API key ID: %w", err)
		}
		id[i] = apiKeyIDAlphabet[n.Int64()]
	}
	return string(id), nil
}

func isValidAPIKeyID(id string) bool {
	if len(id) != apiKeyIDLength {
		return false
	}
	for _, c := range id {
		if !strings.ContainsRune(apiKeyIDAlphabet, c) {
			return false
		}
	}
	return true
}
