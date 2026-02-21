package main

import (
	"os"

	"github.com/aws/aws-lambda-go/lambda"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-body/internal/mcpserver"
)

func serviceVersion() string {
	if v := os.Getenv("SERVICE_VERSION"); v != "" {
		return v
	}
	return "dev"
}

func main() {
	srv, err := mcpserver.New("lesser-body", serviceVersion())
	if err != nil {
		panic(err)
	}

	app := apptheory.New()
	app.Post("/mcp", srv.Handler())

	lambda.Start(app.HandleLambda)
}

