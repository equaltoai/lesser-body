package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

var jwtSecretCache struct {
	once  sync.Once
	value string
	err   error
}

func jwtSecret(ctx context.Context) (string, error) {
	if v := strings.TrimSpace(os.Getenv("JWT_SECRET")); v != "" {
		return v, nil
	}

	secretID := strings.TrimSpace(os.Getenv("JWT_SECRET_ARN"))
	if secretID == "" {
		// Match lesser’s default.
		secretID = "lesser/jwt-secret"
	}

	jwtSecretCache.once.Do(func() {
		jwtSecretCache.value, jwtSecretCache.err = fetchSecretValue(ctx, secretID)
	})

	return jwtSecretCache.value, jwtSecretCache.err
}

var lesserHostInstanceKeyCache struct {
	once  sync.Once
	value string
	err   error
}

func lesserHostInstanceKey(ctx context.Context) (string, error) {
	if v := strings.TrimSpace(os.Getenv("LESSER_HOST_INSTANCE_KEY")); v != "" {
		return v, nil
	}

	secretID := strings.TrimSpace(os.Getenv("LESSER_HOST_INSTANCE_KEY_ARN"))
	if secretID == "" {
		return "", nil
	}

	lesserHostInstanceKeyCache.once.Do(func() {
		lesserHostInstanceKeyCache.value, lesserHostInstanceKeyCache.err = fetchSecretValue(ctx, secretID)
	})

	return lesserHostInstanceKeyCache.value, lesserHostInstanceKeyCache.err
}

func fetchSecretValue(ctx context.Context, secretID string) (string, error) {
	secretID = strings.TrimSpace(secretID)
	if secretID == "" {
		return "", fmt.Errorf("secret id is empty")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return "", fmt.Errorf("load aws config: %w", err)
	}

	sm := secretsmanager.NewFromConfig(cfg)
	out, err := sm.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{SecretId: aws.String(secretID)})
	if err != nil {
		return "", fmt.Errorf("get secret value: %w", err)
	}

	raw := strings.TrimSpace(aws.ToString(out.SecretString))
	if raw == "" && len(out.SecretBinary) > 0 {
		raw = strings.TrimSpace(string(out.SecretBinary))
	}
	if raw == "" {
		return "", errors.New("secret value is empty")
	}

	if strings.HasPrefix(raw, "{") {
		var parsed map[string]string
		if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
			return "", fmt.Errorf("unmarshal secret string: %w", err)
		}

		if v := strings.TrimSpace(parsed["secret"]); v != "" {
			return v, nil
		}
	}

	return raw, nil
}
