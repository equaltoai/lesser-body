package stacks

import (
	"fmt"
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
	jwtSecretArnParamPathParam := awscdk.NewCfnParameter(stack, jsii.String("JWTSecretArnParamPath"), &awscdk.CfnParameterProps{
		Type:        jsii.String("String"),
		Default:     jsii.String(defaultSsmParamPath("lesser", "shared", "secrets", "jwt-secret-arn")),
		Description: jsii.String("SSM parameter path containing the shared JWT secret ARN for the target app."),
	})
	jwtSecretKeyParamPathParam := awscdk.NewCfnParameter(stack, jsii.String("JWTSecretKeyArnParamPath"), &awscdk.CfnParameterProps{
		Type:        jsii.String("String"),
		Default:     jsii.String(defaultSsmParamPath("lesser", "shared", "kms", "encryption-key-arn")),
		Description: jsii.String("SSM parameter path containing the shared KMS key ARN for the target app."),
	})
	lesserStageDomainParamPathParam := awscdk.NewCfnParameter(stack, jsii.String("LesserStageDomainParamPath"), &awscdk.CfnParameterProps{
		Type:        jsii.String("String"),
		Default:     jsii.String(defaultSsmParamPath("lesser", stage, "lesser", "exports", "v1", "domain")),
		Description: jsii.String("SSM parameter path containing the Lesser stage domain for the target app and stage."),
	})
	lesserTableParamPathParam := awscdk.NewCfnParameter(stack, jsii.String("LesserTableNameParamPath"), &awscdk.CfnParameterProps{
		Type:        jsii.String("String"),
		Default:     jsii.String(defaultSsmParamPath("lesser", stage, "lesser", "exports", "v1", "table_name")),
		Description: jsii.String("SSM parameter path containing the Lesser table name for the target app and stage."),
	})

	stageDomain := resolvedStageDomainFromDeployInputs(
		stack,
		stage,
		baseDomainParam.ValueAsString(),
		lesserStageDomainParamPathParam.ValueAsString(),
	)

	configureLesserBodyStack(stack, &lesserBodyRuntimeProps{
		AppName: appNameParam.ValueAsString(),
		Stage:   jsii.String(stage),
		Code: awslambda.Code_FromCfnParameters(&awslambda.CfnParametersCodeProps{
			BucketNameParam: codeBucketParam,
			ObjectKeyParam:  codeKeyParam,
		}),
		ServiceVersion:        jsii.String(serviceVersion),
		PublicEndpoint:        publicMcpEndpoint(stageDomain),
		AllowedOrigins:        mcpAllowedOrigins(stageDomain),
		JWTSecretArnParamPath: jwtSecretArnParamPathParam.ValueAsString(),
		JWTSecretKeyParamPath: jwtSecretKeyParamPathParam.ValueAsString(),
		LesserTableParamPath:  lesserTableParamPathParam.ValueAsString(),
	})

	return &LesserBodyStack{Stack: stack}
}

func resolvedStageDomainFromDeployInputs(stack awscdk.Stack, stage string, baseDomain *string, stageDomainParamPath *string) *string {
	hasBaseDomain := awscdk.NewCfnCondition(stack, jsii.String("HasBaseDomain"), &awscdk.CfnConditionProps{
		Expression: awscdk.Fn_ConditionNot(awscdk.Fn_ConditionEquals(baseDomain, jsii.String(""))),
	})
	stageDomainFromBase := baseDomain
	if stage != "live" {
		stageDomainFromBase = tokenJoin(".", jsii.String(stage), baseDomain)
	}

	domainValue := awscdk.Token_AsString(
		awscdk.NewCfnDynamicReference(awscdk.CfnDynamicReferenceService_SSM, stageDomainParamPath),
		nil,
	)

	return awscdk.Token_AsString(
		awscdk.Fn_ConditionIf(
			hasBaseDomain.LogicalId(),
			stageDomainFromBase,
			domainValue,
		),
		nil,
	)
}

func defaultSsmParamPath(parts ...string) string {
	return fmt.Sprintf("/%s", strings.Join(parts, "/"))
}
