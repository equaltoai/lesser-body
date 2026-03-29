package stacks

import (
	"strings"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslambda"
	"github.com/aws/constructs-go/constructs/v10"
	"github.com/aws/jsii-runtime-go"
)

type LesserBodyDeployTemplateStackProps struct {
	awscdk.StackProps
	ServiceVersion string
	Stage          string
}

func NewLesserBodyDeployTemplateStack(scope constructs.Construct, id string, props *LesserBodyDeployTemplateStackProps) *LesserBodyStack {
	stack := awscdk.NewStack(scope, &id, &props.StackProps)

	serviceVersion := strings.TrimSpace(props.ServiceVersion)
	if serviceVersion == "" {
		serviceVersion = "dev"
	}
	stage := strings.TrimSpace(strings.ToLower(props.Stage))
	switch stage {
	case "dev", "staging", "live":
	default:
		panic("stage must be one of dev, staging, live")
	}

	appNameParam := awscdk.NewCfnParameter(stack, jsii.String("AppName"), &awscdk.CfnParameterProps{
		Type:        jsii.String("String"),
		Default:     jsii.String("lesser"),
		Description: jsii.String("Lesser app slug used in stack naming and SSM paths."),
	})
	baseDomainParam := awscdk.NewCfnParameter(stack, jsii.String("BaseDomain"), &awscdk.CfnParameterProps{
		Type:        jsii.String("String"),
		Default:     jsii.String(""),
		Description: jsii.String("Optional base domain override. Leave empty to use /<app>/<stage>/lesser/exports/v1/domain from SSM."),
	})
	codeBucketParam := awscdk.NewCfnParameter(stack, jsii.String("LesserBodyCodeBucketName"), &awscdk.CfnParameterProps{
		Type:        jsii.String("String"),
		Description: jsii.String("S3 bucket containing the lesser-body Lambda zip release asset."),
	})
	codeKeyParam := awscdk.NewCfnParameter(stack, jsii.String("LesserBodyCodeObjectKey"), &awscdk.CfnParameterProps{
		Type:        jsii.String("String"),
		Description: jsii.String("S3 object key for the lesser-body Lambda zip release asset."),
	})

	stageDomain := resolvedStageDomainFromDeployInputs(
		stack,
		appNameParam.ValueAsString(),
		stage,
		baseDomainParam.ValueAsString(),
	)

	configureLesserBodyStack(stack, &lesserBodyRuntimeProps{
		AppName: appNameParam.ValueAsString(),
		Stage:   jsii.String(stage),
		Code: awslambda.Code_FromCfnParameters(&awslambda.CfnParametersCodeProps{
			BucketNameParam: codeBucketParam,
			ObjectKeyParam:  codeKeyParam,
		}),
		ServiceVersion: jsii.String(serviceVersion),
		PublicEndpoint: publicMcpEndpoint(stageDomain),
		AllowedOrigins: mcpAllowedOrigins(stageDomain),
	})

	return &LesserBodyStack{Stack: stack}
}

func resolvedStageDomainFromDeployInputs(stack awscdk.Stack, appName *string, stage string, baseDomain *string) *string {
	hasBaseDomain := awscdk.NewCfnCondition(stack, jsii.String("HasBaseDomain"), &awscdk.CfnConditionProps{
		Expression: awscdk.Fn_ConditionNot(awscdk.Fn_ConditionEquals(baseDomain, jsii.String(""))),
	})
	stageDomainFromBase := baseDomain
	if stage != "live" {
		stageDomainFromBase = tokenJoin(".", jsii.String(stage), baseDomain)
	}

	domainParam := importStringParameter(
		stack,
		jsii.String("LesserStageDomainParamLookup"),
		ssmParamName(appName, jsii.String(stage), jsii.String("lesser"), jsii.String("exports"), jsii.String("v1"), jsii.String("domain")),
	)

	return awscdk.Token_AsString(
		awscdk.Fn_ConditionIf(
			hasBaseDomain.LogicalId(),
			stageDomainFromBase,
			domainParam.StringValue(),
		),
		nil,
	)
}
