package main

import (
	"context"
	"encoding/json"
	"os"

	"github.com/aws/aws-lambda-go/lambda"

	"github.com/equaltoai/lesser-body/internal/instanceapp"
)

func serviceVersion() string {
	if v := os.Getenv("SERVICE_VERSION"); v != "" {
		return v
	}
	return "dev"
}

func main() {
	app, err := instanceapp.New("lesser-body-instance", serviceVersion())
	if err != nil {
		panic(err)
	}

	lambda.Start(func(ctx context.Context, event json.RawMessage) (any, error) {
		return app.HandleLambda(ctx, event)
	})
}
