package auth

import (
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/EdgeCDN-X/edgecdnx-api/src/internal/logger"
	infrastructurev1alpha1 "github.com/EdgeCDN-X/edgecdnx-controller/api/v1alpha1"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
)

// Gin context keys set by AuthMiddleware.
const (
	ContextKeyUserID        = "user_id"
	ContextKeyGroups        = "groups"
	ContextKeyClaims        = "claims"
	ContextKeyAuthMethod    = "auth_method"
	ContextKeyAPIKeyProject = "apikey_project"

	AuthMethodOIDC   = "oidc"
	AuthMethodAPIKey = "apikey"

	authSchemeBearer = "bearer"
	authSchemeAPIKey = "apikey"
)

var errInvalidAPIKey = errors.New("invalid API key")

// IsAPIKeyRequest reports whether the request was authenticated with an API key.
func IsAPIKeyRequest(c *gin.Context) bool {
	return c.GetString(ContextKeyAuthMethod) == AuthMethodAPIKey
}

// APIKeyProject returns the project an API key request is confined to.
func APIKeyProject(c *gin.Context) (string, bool) {
	if !IsAPIKeyRequest(c) {
		return "", false
	}
	return c.GetString(ContextKeyAPIKeyProject), true
}

// parseAuthorization splits an Authorization header into a lower-cased scheme and its credentials.
func parseAuthorization(header string) (scheme string, credentials string, ok bool) {
	scheme, credentials, ok = strings.Cut(header, " ")
	credentials = strings.TrimSpace(credentials)
	if !ok || scheme == "" || credentials == "" {
		return "", "", false
	}
	return strings.ToLower(scheme), credentials, true
}

// parseAPIKeyToken validates the token shape (ak_<id>_<base64url secret>) and returns the key ID.
func parseAPIKeyToken(token string) (string, bool) {
	rest, ok := strings.CutPrefix(token, APIKeySubjectPrefix)
	if !ok {
		return "", false
	}
	id, secret, ok := strings.Cut(rest, "_")
	if !ok || !isValidAPIKeyID(id) {
		return "", false
	}
	if base64.RawURLEncoding.EncodedLen(apiKeySecretBytes) != len(secret) {
		return "", false
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(secret)
	if err != nil || len(decoded) != apiKeySecretBytes {
		return "", false
	}
	return id, true
}

type apiKeyPrincipal struct {
	Subject string
	Project string
}

// authenticateAPIKey resolves a token against the APIKey cache. All failures return
// errInvalidAPIKey so callers cannot distinguish unknown, revoked, or expired keys.
func (m *Module) authenticateAPIKey(token string) (apiKeyPrincipal, error) {
	id, ok := parseAPIKeyToken(token)
	if !ok {
		return apiKeyPrincipal{}, rejectAPIKey("", "malformed token")
	}
	if m.apiKeys == nil || m.projects == nil {
		return apiKeyPrincipal{}, fmt.Errorf("API key authentication is not initialized")
	}

	name := APIKeyObjectName(id)
	key, err := getCached[infrastructurev1alpha1.APIKey](m.apiKeys, m.cfg.Namespace, name)
	if err != nil {
		return apiKeyPrincipal{}, err
	}
	if key == nil {
		return apiKeyPrincipal{}, rejectAPIKey(name, "not found")
	}

	cred := key.Spec.Credential
	if cred.FormatVersion != APIKeyFormatVersion || cred.Algorithm != APIKeyDigestAlgorithm {
		return apiKeyPrincipal{}, rejectAPIKey(name, "unsupported credential format")
	}
	if subtle.ConstantTimeCompare([]byte(APIKeyDigest(token)), []byte(cred.Digest)) != 1 {
		return apiKeyPrincipal{}, rejectAPIKey(name, "digest mismatch")
	}
	if key.DeletionTimestamp != nil {
		return apiKeyPrincipal{}, rejectAPIKey(name, "being deleted")
	}
	if key.Spec.ExpiresAt != nil && !m.now().Before(key.Spec.ExpiresAt.Time) {
		return apiKeyPrincipal{}, rejectAPIKey(name, "expired")
	}

	if key.Spec.ProjectRef == nil || key.Spec.ProjectRef.Name == "" {
		return apiKeyPrincipal{}, rejectAPIKey(name, "missing projectRef")
	}
	projectName := key.Spec.ProjectRef.Name
	if key.Labels[APIKeyProjectLabel] != projectName {
		return apiKeyPrincipal{}, rejectAPIKey(name, "project label does not match projectRef")
	}

	project, err := getCached[infrastructurev1alpha1.Project](m.projects, m.cfg.Namespace, projectName)
	if err != nil {
		return apiKeyPrincipal{}, err
	}
	if project == nil || project.DeletionTimestamp != nil {
		return apiKeyPrincipal{}, rejectAPIKey(name, "project not found")
	}
	// Bind to the project incarnation so a deleted and recreated project does not inherit old keys.
	if !ownedByProject(key, project) {
		return apiKeyPrincipal{}, rejectAPIKey(name, "project owner reference does not match")
	}

	return apiKeyPrincipal{Subject: APIKeySubject(id), Project: projectName}, nil
}

func ownedByProject(key *infrastructurev1alpha1.APIKey, project *infrastructurev1alpha1.Project) bool {
	for _, ref := range key.OwnerReferences {
		if ref.Kind == "Project" && ref.Name == project.Name && ref.UID == project.UID {
			return true
		}
	}
	return false
}

func rejectAPIKey(name, reason string) error {
	logger.L().Debug("API key rejected", zap.String("apikey", name), zap.String("reason", reason))
	return errInvalidAPIKey
}

func getCached[T any](store cache.Store, namespace, name string) (*T, error) {
	key := name
	if namespace != "" {
		key = namespace + "/" + name
	}
	obj, exists, err := store.GetByKey(key)
	if err != nil {
		return nil, fmt.Errorf("read cache: %w", err)
	}
	if !exists {
		return nil, nil
	}
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return nil, fmt.Errorf("unexpected cached object type %T", obj)
	}
	out := new(T)
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, out); err != nil {
		return nil, fmt.Errorf("convert cached object: %w", err)
	}
	return out, nil
}

func (m *Module) handleAPIKeyAuth(c *gin.Context, token string) {
	principal, err := m.authenticateAPIKey(token)
	if err != nil {
		if errors.Is(err, errInvalidAPIKey) {
			c.Header("WWW-Authenticate", `ApiKey realm="edgecdnx"`)
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": errInvalidAPIKey.Error()})
			return
		}
		logger.L().Error("API key authentication failed", zap.Error(err))
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	// API keys are confined to their project regardless of RBAC bindings elsewhere.
	if projectID := c.Param("project-id"); projectID != "" && projectID != principal.Project {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	c.Set(ContextKeyAuthMethod, AuthMethodAPIKey)
	c.Set(ContextKeyUserID, principal.Subject)
	c.Set(ContextKeyAPIKeyProject, principal.Project)
	c.Next()
}

func (m *Module) now() time.Time {
	if m.clock != nil {
		return m.clock()
	}
	return time.Now()
}
