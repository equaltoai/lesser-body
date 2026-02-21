package auth

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	apptheory "github.com/theory-cloud/apptheory/runtime"
)

const contextKeyPrincipal = "lesser_body_principal"

type PrincipalType string

const (
	PrincipalTypeOAuthToken  PrincipalType = "oauth_token"
	PrincipalTypeInstanceKey PrincipalType = "instance_key"
)

type Principal struct {
	Type     PrincipalType
	Identity string
	Claims   *Claims
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

		// Fall back to managed instance key authentication (operator automation).
		instanceKey, keyErr := lesserHostInstanceKey(ctx.Context())
		if keyErr == nil && instanceKey != "" && TimingSafeTokenValidation(token, instanceKey) {
			identity := "instance"
			WithPrincipal(ctx, &Principal{
				Type:     PrincipalTypeInstanceKey,
				Identity: identity,
				Claims:   nil,
			})
			return identity, nil
		}

		return "", &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}
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

	// Match Lesser’s maximum token age safety check (independent of exp).
	if claims.IssuedAt != nil {
		if time.Since(claims.IssuedAt.Time) > 24*time.Hour {
			return nil, &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
		}
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
}
