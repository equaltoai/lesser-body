package main

import (
	"os"

	"github.com/aws/aws-lambda-go/lambda"

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

	lambda.Start(app.HandleLambda)
}
