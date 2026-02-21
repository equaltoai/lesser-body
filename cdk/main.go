package main

import (
	"fmt"
	"os"
	"strings"

	"cdk/stacks"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/jsii-runtime-go"
)

func main() {
	defer jsii.Close()

	app := awscdk.NewApp(nil)

	appName := getContextString(app, "app")
	if appName == "" {
		appName = "lesser"
	}

	stage := strings.TrimSpace(strings.ToLower(getContextString(app, "stage")))
	if stage == "" {
		stage = "dev"
	}
	switch stage {
	case "dev", "staging", "live":
	default:
		fmt.Printf("Error: invalid stage %q (expected dev, staging, live)\n", stage)
		os.Exit(1)
	}

	baseDomain := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(getContextString(app, "baseDomain"))), ".")
	hostedZoneId := strings.TrimSpace(getContextString(app, "hostedZoneId"))

	awsAccount := strings.TrimSpace(os.Getenv("CDK_DEFAULT_ACCOUNT"))
	awsRegion := strings.TrimSpace(os.Getenv("CDK_DEFAULT_REGION"))
	if awsRegion == "" {
		awsRegion = strings.TrimSpace(os.Getenv("AWS_REGION"))
	}
	if awsAccount == "" {
		fmt.Println("Error: CDK_DEFAULT_ACCOUNT is not set (run via the CDK CLI)")
		os.Exit(1)
	}
	if awsRegion == "" {
		fmt.Println("Error: CDK_DEFAULT_REGION is not set (run via the CDK CLI)")
		os.Exit(1)
	}

	env := &awscdk.Environment{
		Account: jsii.String(awsAccount),
		Region:  jsii.String(awsRegion),
	}

	stackName := fmt.Sprintf("%s-%s-lesser-body", appName, stage)
	_ = stacks.NewLesserBodyStack(app, stackName, &stacks.LesserBodyStackProps{
		StackProps: awscdk.StackProps{
			Env: env,
		},
		AppName:      appName,
		Stage:        stage,
		BaseDomain:   baseDomain,
		HostedZoneId: hostedZoneId,
	})

	app.Synth(nil)
}

func getContextString(app awscdk.App, key string) string {
	value := app.Node().TryGetContext(jsii.String(key))
	if value == nil {
		return ""
	}
	out := strings.TrimSpace(fmt.Sprintf("%v", value))
	if out == "" || strings.EqualFold(out, "<nil>") {
		return ""
	}
	return out
}
