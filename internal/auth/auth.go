package auth

import (
	"context"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/equaltoai/lesser-body/internal/trustconfig"
	"github.com/golang-jwt/jwt/v5"
	apptheory "github.com/theory-cloud/apptheory/v4/runtime"
	oauthruntime "github.com/theory-cloud/apptheory/v4/runtime/oauth"
)

const (
	contextKeyPrincipal = "lesser_body_principal"
	maxAccessTokenAge   = 24 * time.Hour
)

type PrincipalType string

const (
	PrincipalTypeOAuthToken  PrincipalType = "oauth_token"
	PrincipalTypeInstanceKey PrincipalType = "instance_key"
	PrincipalTypeX402Grant   PrincipalType = "x402_grant"
)

// BearerCredentialClass records whether a request supplied a syntactically
// usable bearer credential. It deliberately does not validate the token.
type BearerCredentialClass uint8

const (
	BearerCredentialAbsent BearerCredentialClass = iota
	BearerCredentialPresented
)

const legacyInstanceKeyInboundAuthEnv = "MCP_ALLOW_LEGACY_INSTANCE_KEY"

type Principal struct {
	Type      PrincipalType
	Identity  string
	Claims    *Claims
	X402Grant *X402InvocationGrant
}

type X402InvocationGrant struct {
	GrantIDHash         string
	AgentID             string
	Actor               string
	Tool                string
	Capability          string
	Scope               string
	GrantVersion        string
	PolicyVersion       string
	Resource            string
	RequestHash         string
	PaymentEvidenceHash string
	CallerEvidenceHash  string
	ExpiresAt           time.Time
	MaxUses             int
	RemainingUses       int
}

func PrincipalFromContext(ctx *apptheory.Context) *Principal {
	if ctx == nil {
		return nil
	}
	val := ctx.Get(contextKeyPrincipal)
	p, ok := val.(*Principal)
	if !ok {
		return nil
	}
	return p
}

func WithPrincipal(ctx *apptheory.Context, principal *Principal) {
	if ctx == nil || principal == nil {
		return
	}
	ctx.Set(contextKeyPrincipal, principal)
}

func Hook(logger *slog.Logger) apptheory.AuthHook {
	if logger == nil {
		logger = slog.Default()
	}

	return func(ctx *apptheory.Context) (string, error) {
		token, ok := bearerToken(ctx)
		if !ok {
			return "", nil
		}

		// Prefer JWT (OAuth access token) validation.
		claims, err := validateAccessToken(ctx.Context(), token)
		if err == nil && claims != nil {
			identity := strings.TrimSpace(claims.GetUsername())
			if identity == "" {
				return "", nil
			}
			WithPrincipal(ctx, &Principal{
				Type:     PrincipalTypeOAuthToken,
				Identity: identity,
				Claims:   claims,
			})
			return identity, nil
		}

		// Fall back to managed instance key authentication only when the temporary
		// compatibility flag is explicitly enabled.
		if LegacyInstanceKeyInboundAuthEnabled() {
			instanceKey, keyErr := lesserHostInstanceKey(ctx.Context())
			if keyErr == nil && instanceKey != "" && TimingSafeTokenValidation(token, instanceKey) {
				identity := "instance"
				logger.Warn("managed-instance-key inbound MCP auth is deprecated; migrate clients to the OAuth connector flow",
					"path", strings.TrimSpace(ctx.Request.Path),
					"migration_doc", "docs/oauth-migration.md",
					"compatibility_flag", legacyInstanceKeyInboundAuthEnv,
				)
				WithPrincipal(ctx, &Principal{
					Type:     PrincipalTypeInstanceKey,
					Identity: identity,
					Claims:   nil,
				})
				return identity, nil
			}
		}

		return "", &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}
}

func LegacyInstanceKeyInboundAuthEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(legacyInstanceKeyInboundAuthEnv))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// BearerTokenFromRequest returns the raw OAuth bearer token carried in the
// request Authorization header, or false when the header is absent or
// malformed. It performs no validation; callers that need identity must use
// Hook or PrincipalFromContext.
func BearerTokenFromRequest(ctx *apptheory.Context) (string, bool) {
	return bearerToken(ctx)
}

// BearerCredentialClassFromRequest distinguishes OAuth discovery from a
// rejected credential without inspecting validation error strings. Callers
// should use this only after authentication has failed.
func BearerCredentialClassFromRequest(ctx *apptheory.Context) BearerCredentialClass {
	if _, ok := bearerToken(ctx); ok {
		return BearerCredentialPresented
	}
	return BearerCredentialAbsent
}

func bearerToken(ctx *apptheory.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	for key, values := range ctx.Request.Headers {
		if !strings.EqualFold(strings.TrimSpace(key), "authorization") {
			continue
		}
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			parts := strings.Fields(value)
			if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
				return "", false
			}
			return strings.TrimSpace(parts[1]), true
		}
	}
	return "", false
}

type Claims struct {
	jwt.RegisteredClaims
	Username string   `json:"username"`
	Scopes   []string `json:"scopes"`
	ClientID string   `json:"client_id"`

	// Context fields used by policy decisions in Lesser.
	ClientClass    string `json:"client_class,omitempty"`
	SessionID      string `json:"sid,omitempty"`
	DeviceID       string `json:"did,omitempty"`
	TokenVersion   int    `json:"tv,omitempty"`
	IPAddress      string `json:"ip,omitempty"`
	UserAgent      string `json:"ua,omitempty"`
	IsAgent        bool   `json:"is_agent,omitempty"`
	AgentType      string `json:"agent_type,omitempty"`
	DelegatedBy    string `json:"delegated_by,omitempty"`
	AgentSessionID string `json:"agent_session_id,omitempty"`
}

func (c *Claims) GetUsername() string {
	if c == nil {
		return ""
	}
	return c.Username
}

func ValidateTokenAudience(claims *Claims, expectedAudience string) error {
	expectedAudience = oauthruntime.CanonicalResourceURL(expectedAudience)
	if expectedAudience == "" || claims == nil {
		return nil
	}

	audiences := make([]string, 0, len(claims.Audience))
	for _, audience := range claims.Audience {
		if canonical := oauthruntime.CanonicalResourceURL(audience); canonical != "" {
			audiences = append(audiences, canonical)
		}
	}
	if slices.Contains(audiences, expectedAudience) {
		return nil
	}

	return &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
}

func validateAccessToken(ctx context.Context, tokenString string) (*Claims, error) {
	secret, err := jwtSecret(ctx)
	if err != nil {
		return nil, err
	}
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return nil, &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}

	token, err := jwt.ParseWithClaims(
		tokenString,
		&Claims{},
		func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
			}
			return []byte(secret), nil
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Name}),
	)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}
	if claims.IssuedAt == nil || claims.IssuedAt.Time.IsZero() {
		return nil, &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}
	if time.Since(claims.IssuedAt.Time) > maxAccessTokenAge {
		return nil, &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}

	return claims, nil
}

func ResetForTests() {
	jwtSecretCache = struct {
		once  sync.Once
		value string
		err   error
	}{}
	lesserHostInstanceKeyCache = struct {
		once  sync.Once
		value string
		err   error
	}{}
	loadEffectiveTrustConfig = trustconfig.Default
}
