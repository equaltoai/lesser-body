package stacks

import (
	"fmt"
	"strings"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslambda"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsssm"
	"github.com/aws/constructs-go/constructs/v10"
	"github.com/aws/jsii-runtime-go"
)

type LesserBodyStackProps struct {
	awscdk.StackProps
	AppName    string
	Stage      string // dev|staging|live
	BaseDomain string // optional; if set, compute api.<stageDomain> without SSM lookup
}

type LesserBodyStack struct {
	awscdk.Stack
}

func NewLesserBodyStack(scope constructs.Construct, id string, props *LesserBodyStackProps) *LesserBodyStack {
	stack := awscdk.NewStack(scope, &id, &props.StackProps)

	appName := strings.TrimSpace(props.AppName)
	if appName == "" {
		appName = "lesser"
	}
	stage := strings.TrimSpace(strings.ToLower(props.Stage))
	if stage == "" {
		stage = "dev"
	}
	stageDomain := resolvedStageDomain(stack, appName, stage, props.BaseDomain)
	configureLesserBodyStack(stack, &lesserBodyRuntimeProps{
		AppName:        jsii.String(appName),
		Stage:          jsii.String(stage),
		Code:           awslambda.Code_FromAsset(jsii.String("../dist/lesser-body.zip"), nil),
		ServiceVersion: jsii.String("dev"),
		PublicEndpoint: publicMcpEndpoint(stageDomain),
		AllowedOrigins: mcpAllowedOrigins(stageDomain),
	})

	return &LesserBodyStack{Stack: stack}
}

func resolvedStageDomain(stack awscdk.Stack, appName string, stage string, baseDomain string) *string {
	if strings.TrimSpace(baseDomain) != "" {
		return jsii.String(stageDomainFor(stage, baseDomain))
	}
	paramName := fmt.Sprintf("/%s/%s/lesser/exports/v1/domain", appName, stage)
	domainParam := awsssm.StringParameter_FromStringParameterName(stack, jsii.String("LesserStageDomainParamLookup"), jsii.String(paramName))
	return domainParam.StringValue()
}

func publicMcpEndpoint(stageDomain *string) *string {
	return awscdk.Fn_Join(jsii.String(""), &[]*string{
		jsii.String("https://api."),
		stageDomain,
		jsii.String("/mcp/{actor}"),
	})
}

func mcpAllowedOrigins(stageDomain *string) *string {
	return awscdk.Fn_Join(jsii.String(""), &[]*string{
		jsii.String("https://claude.ai,https://claude.com,https://"),
		stageDomain,
		jsii.String(",https://app."),
		stageDomain,
		jsii.String(",https://api."),
		stageDomain,
	})
}

func stageDomainFor(stage string, baseDomain string) string {
	base := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(baseDomain)), ".")
	if base == "" {
		return ""
	}
	if stage == "live" {
		return base
	}
	return fmt.Sprintf("%s.%s", stage, base)
}
