package main

import (
	"context"
	"os"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"

	"github.com/equaltoai/lesser-body/internal/lambdaentry"
	"github.com/equaltoai/lesser-body/internal/mcpapp"
)

func serviceVersion() string {
	if v := os.Getenv("SERVICE_VERSION"); v != "" {
		return v
	}
	return "dev"
}

func main() {
	app, err := mcpapp.New("lesser-body", serviceVersion())
	if err != nil {
		panic(err)
	}

	handler := lambdaentry.NewAPIGatewayHandler(app)
	lambda.Start(func(ctx context.Context, event events.APIGatewayProxyRequest) (any, error) { return handler(ctx, event) })
}
