package stacks

import (
	"strings"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsiam"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslambda"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslogs"
	"github.com/aws/aws-cdk-go/awscdk/v2/awssecretsmanager"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsssm"
	"github.com/aws/constructs-go/constructs/v10"
	"github.com/aws/jsii-runtime-go"
	apptheorycdk "github.com/theory-cloud/apptheory/cdk-go/apptheorycdk"
)

type lesserBodyRuntimeProps struct {
	AppName                  *string
	Stage                    *string
	Code                     awslambda.Code
	ServiceVersion           *string
	PublicEndpoint           *string
	LesserAPIBaseURL         *string
	AllowedOrigins           *string
	JWTSecretArnParamPath    *string
	JWTSecretKeyParamPath    *string
	LesserTableParamPath     *string
	LesserHostInstanceKeyARN *string
}

func configureLesserBodyStack(stack awscdk.Stack, props *lesserBodyRuntimeProps) {
	serviceVersion := props.ServiceVersion
	if serviceVersion == nil || *serviceVersion == "" {
		serviceVersion = jsii.String("dev")
	}

	handler := awslambda.NewFunction(stack, jsii.String("McpHandler"), &awslambda.FunctionProps{
		Runtime:      awslambda.Runtime_PROVIDED_AL2023(),
		Architecture: awslambda.Architecture_ARM_64(),
		Handler:      jsii.String("bootstrap"),
		Code:         props.Code,
		FunctionName: tokenJoin("-", props.AppName, props.Stage, jsii.String("lesser-body"), jsii.String("mcp")),
		MemorySize:   jsii.Number(1024),
		Timeout:      awscdk.Duration_Seconds(jsii.Number(30)),
		Tracing:      awslambda.Tracing_ACTIVE,
		Environment: &map[string]*string{
			"SERVICE_VERSION": serviceVersion,
		},
	})

	jwtSecretArnParamPath := props.JWTSecretArnParamPath
	if jwtSecretArnParamPath == nil || *jwtSecretArnParamPath == "" {
		jwtSecretArnParamPath = ssmParamName(props.AppName, jsii.String("shared"), jsii.String("secrets"), jsii.String("jwt-secret-arn"))
	}
	jwtSecretKeyParamPath := props.JWTSecretKeyParamPath
	if jwtSecretKeyParamPath == nil || *jwtSecretKeyParamPath == "" {
		jwtSecretKeyParamPath = ssmParamName(props.AppName, jsii.String("shared"), jsii.String("kms"), jsii.String("encryption-key-arn"))
	}
	jwtSecretArnValue := lookupStringParameterValue(stack, jsii.String("JWTSecretArnParamLookup"), props.JWTSecretArnParamPath, jwtSecretArnParamPath)
	jwtSecretKeyArnValue := lookupStringParameterValue(stack, jsii.String("JWTSecretKeyArnParamLookup"), props.JWTSecretKeyParamPath, jwtSecretKeyParamPath)
	handler.AddEnvironment(jsii.String("JWT_SECRET_ARN"), jwtSecretArnValue, nil)
	jwtSecret := awssecretsmanager.Secret_FromSecretCompleteArn(
		stack,
		jsii.String("ImportedJWTSecret"),
		jwtSecretArnValue,
	)
	jwtSecret.GrantRead(handler, nil)
	handler.AddToRolePolicy(awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
		Actions: &[]*string{
			jsii.String("kms:Decrypt"),
			jsii.String("kms:DescribeKey"),
		},
		Resources: &[]*string{
			jwtSecretKeyArnValue,
		},
	}))

	if props.LesserHostInstanceKeyARN != nil {
		handler.AddEnvironment(jsii.String("LESSER_HOST_INSTANCE_KEY_ARN"), props.LesserHostInstanceKeyARN, nil)
	}

	instanceKeySecretResources := []*string{
		legacyInstanceKeySecretArnPattern(stack, props.AppName),
		managedLesserHostInstanceKeySecretArnPattern(stack, props.Stage, props.AppName),
	}
	if exactSecretArn := optionalNonEmptyStringValue(stack, jsii.String("HasLesserHostInstanceKeyARN"), props.LesserHostInstanceKeyARN); exactSecretArn != nil {
		instanceKeySecretResources = append(instanceKeySecretResources, exactSecretArn)
	}

	handler.AddToRolePolicy(awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
		Actions: &[]*string{
			jsii.String("secretsmanager:GetSecretValue"),
			jsii.String("secretsmanager:DescribeSecret"),
		},
		Resources: &instanceKeySecretResources,
	}))

	mcpProps := &apptheorycdk.AppTheoryRemoteMcpServerProps{
		Handler:                     handler,
		ApiName:                     tokenJoin("-", props.AppName, props.Stage, jsii.String("mcp")),
		ActorPath:                   jsii.Bool(true),
		EnableWellKnownMcpDiscovery: jsii.Bool(true),
		Cors: &apptheorycdk.AppTheoryRestApiRouterCorsOptions{
			AllowOrigins: &[]*string{
				jsii.String("*"),
			},
			AllowHeaders: &[]*string{
				jsii.String("authorization"),
				jsii.String("content-type"),
				jsii.String("mcp-protocol-version"),
				jsii.String("mcp-session-id"),
				jsii.String("last-event-id"),
			},
			AllowMethods: &[]*string{
				jsii.String("GET"),
				jsii.String("POST"),
				jsii.String("DELETE"),
				jsii.String("OPTIONS"),
			},
		},
		EnableSessionTable: jsii.Bool(true),
		SessionTableName:   tokenJoin("-", props.AppName, props.Stage, jsii.String("mcp"), jsii.String("sessions")),
		SessionTtlMinutes:  jsii.Number(60),
		EnableStreamTable:  jsii.Bool(true),
		StreamTableName:    tokenJoin("-", props.AppName, props.Stage, jsii.String("mcp"), jsii.String("streams"), jsii.String("v2")),
		StreamTtlMinutes:   jsii.Number(60),
		Stage: &apptheorycdk.AppTheoryRestApiRouterStageOptions{
			StageName:          props.Stage,
			AccessLogging:      true,
			AccessLogRetention: awslogs.RetentionDays_ONE_WEEK,
		},
	}

	handler.AddToRolePolicy(awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
		Actions: &[]*string{
			jsii.String("ssm:GetParameter"),
			jsii.String("ssm:GetParameters"),
			jsii.String("ssm:GetParametersByPath"),
		},
		Resources: &[]*string{
			stack.FormatArn(&awscdk.ArnComponents{
				Service:      jsii.String("ssm"),
				Resource:     jsii.String("parameter"),
				ResourceName: ssmResourceName(props.AppName, props.Stage, jsii.String("lesser"), jsii.String("exports"), jsii.String("v1"), jsii.String("*")),
			}),
			stack.FormatArn(&awscdk.ArnComponents{
				Service:      jsii.String("ssm"),
				Resource:     jsii.String("parameter"),
				ResourceName: ssmResourceName(props.AppName, props.Stage, jsii.String("lesser-soul"), jsii.String("exports"), jsii.String("v1"), jsii.String("*")),
			}),
		},
	}))

	server := apptheorycdk.NewAppTheoryRemoteMcpServer(stack, jsii.String("McpServer"), mcpProps)

	handler.AddEnvironment(jsii.String("MCP_ENDPOINT"), props.PublicEndpoint, nil)
	if props.LesserAPIBaseURL != nil && *props.LesserAPIBaseURL != "" {
		handler.AddEnvironment(jsii.String("LESSER_API_BASE_URL"), props.LesserAPIBaseURL, nil)
	}
	handler.AddEnvironment(jsii.String("MCP_ALLOWED_ORIGINS"), props.AllowedOrigins, nil)

	lesserTableParamPath := props.LesserTableParamPath
	if lesserTableParamPath == nil || *lesserTableParamPath == "" {
		lesserTableParamPath = ssmParamName(props.AppName, props.Stage, jsii.String("lesser"), jsii.String("exports"), jsii.String("v1"), jsii.String("table_name"))
	}
	tableName := lookupStringParameterValue(stack, jsii.String("LesserTableNameParamLookup"), props.LesserTableParamPath, lesserTableParamPath)
	handler.AddEnvironment(jsii.String("LESSER_TABLE_NAME"), tableName, nil)
	tableArn := stack.FormatArn(&awscdk.ArnComponents{
		Service:      jsii.String("dynamodb"),
		Resource:     jsii.String("table"),
		ResourceName: tableName,
	})
	indexArn := stack.FormatArn(&awscdk.ArnComponents{
		Service:      jsii.String("dynamodb"),
		Resource:     jsii.String("table"),
		ResourceName: tokenJoin("/", tableName, jsii.String("index"), jsii.String("*")),
	})
	handler.AddToRolePolicy(awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
		Actions: &[]*string{
			jsii.String("dynamodb:BatchGetItem"),
			jsii.String("dynamodb:Query"),
			jsii.String("dynamodb:GetItem"),
			jsii.String("dynamodb:Scan"),
			jsii.String("dynamodb:ConditionCheckItem"),
			jsii.String("dynamodb:BatchWriteItem"),
			jsii.String("dynamodb:PutItem"),
			jsii.String("dynamodb:UpdateItem"),
			jsii.String("dynamodb:DeleteItem"),
			jsii.String("dynamodb:DescribeTable"),
		},
		Resources: &[]*string{tableArn, indexArn},
	}))

	mcpLambdaArnParam := awsssm.NewCfnParameter(stack, jsii.String("McpLambdaArnParam"), &awsssm.CfnParameterProps{
		Name:  ssmParamName(props.AppName, props.Stage, jsii.String("lesser-body"), jsii.String("exports"), jsii.String("v1"), jsii.String("mcp_lambda_arn")),
		Type:  jsii.String("String"),
		Value: handler.FunctionArn(),
	})
	mcpLambdaArnParam.OverrideLogicalId(jsii.String("McpLambdaArnParamE9C053F0"))

	mcpEndpointParam := awsssm.NewCfnParameter(stack, jsii.String("McpEndpointParam"), &awsssm.CfnParameterProps{
		Name:  ssmParamName(props.AppName, props.Stage, jsii.String("lesser-body"), jsii.String("exports"), jsii.String("v1"), jsii.String("mcp_endpoint_url")),
		Type:  jsii.String("String"),
		Value: props.PublicEndpoint,
	})
	mcpEndpointParam.OverrideLogicalId(jsii.String("McpEndpointParam71B07820"))
	if server.SessionTable() != nil {
		mcpSessionTableParam := awsssm.NewCfnParameter(stack, jsii.String("McpSessionTableParam"), &awsssm.CfnParameterProps{
			Name:  ssmParamName(props.AppName, props.Stage, jsii.String("lesser-body"), jsii.String("exports"), jsii.String("v1"), jsii.String("mcp_session_table_name")),
			Type:  jsii.String("String"),
			Value: server.SessionTable().TableName(),
		})
		mcpSessionTableParam.OverrideLogicalId(jsii.String("McpSessionTableParam11A03692"))
	}
	if server.StreamTable() != nil {
		mcpStreamTableParam := awsssm.NewCfnParameter(stack, jsii.String("McpStreamTableParam"), &awsssm.CfnParameterProps{
			Name:  ssmParamName(props.AppName, props.Stage, jsii.String("lesser-body"), jsii.String("exports"), jsii.String("v1"), jsii.String("mcp_stream_table_name")),
			Type:  jsii.String("String"),
			Value: server.StreamTable().TableName(),
		})
		mcpStreamTableParam.OverrideLogicalId(jsii.String("McpStreamTableParam604E9EFA"))
	}
}

func ssmParamName(parts ...*string) *string {
	values := make([]*string, 0, len(parts)+1)
	values = append(values, jsii.String(""))
	values = append(values, parts...)
	return awscdk.Fn_Join(jsii.String("/"), &values)
}

func ssmResourceName(parts ...*string) *string {
	return awscdk.Fn_Join(jsii.String("/"), &parts)
}

func tokenJoin(delimiter string, parts ...*string) *string {
	return awscdk.Fn_Join(jsii.String(delimiter), &parts)
}

func importStringParameter(scope constructs.Construct, id *string, parameterName *string) awsssm.IStringParameter {
	return awsssm.StringParameter_FromStringParameterAttributes(scope, id, &awsssm.StringParameterAttributes{
		ParameterName: parameterName,
		SimpleName:    jsii.Bool(false),
	})
}

func lookupStringParameterValue(stack awscdk.Stack, id *string, explicitPath *string, fallbackPath *string) *string {
	if explicitPath != nil && *explicitPath != "" {
		return awscdk.Token_AsString(
			awscdk.NewCfnDynamicReference(awscdk.CfnDynamicReferenceService_SSM, explicitPath),
			nil,
		)
	}
	return importStringParameter(stack, id, fallbackPath).StringValue()
}

func legacyInstanceKeySecretArnPattern(stack awscdk.Stack, appName *string) *string {
	return stack.FormatArn(&awscdk.ArnComponents{
		Service:      jsii.String("secretsmanager"),
		Resource:     jsii.String("secret"),
		ArnFormat:    awscdk.ArnFormat_COLON_RESOURCE_NAME,
		ResourceName: tokenJoin("/", appName, jsii.String("instance-key*")),
	})
}

func managedLesserHostInstanceKeySecretArnPattern(stack awscdk.Stack, stage *string, appName *string) *string {
	return stack.FormatArn(&awscdk.ArnComponents{
		Service:   jsii.String("secretsmanager"),
		Resource:  jsii.String("secret"),
		ArnFormat: awscdk.ArnFormat_COLON_RESOURCE_NAME,
		ResourceName: tokenJoin(
			"/",
			jsii.String("lesser-host"),
			lesserHostControlPlaneStage(stage),
			jsii.String("instances"),
			appName,
			jsii.String("instance-key*"),
		),
	})
}

func lesserHostControlPlaneStage(stage *string) *string {
	normalized := ""
	if stage != nil {
		normalized = strings.ToLower(strings.TrimSpace(*stage))
	}
	switch normalized {
	case "live":
		return jsii.String("live")
	case "staging":
		return jsii.String("staging")
	default:
		return jsii.String("lab")
	}
}

func optionalNonEmptyStringValue(stack awscdk.Stack, id *string, value *string) *string {
	if value == nil {
		return nil
	}
	hasValue := awscdk.NewCfnCondition(stack, id, &awscdk.CfnConditionProps{
		Expression: awscdk.Fn_ConditionNot(awscdk.Fn_ConditionEquals(value, jsii.String(""))),
	})
	return awscdk.Token_AsString(
		awscdk.Fn_ConditionIf(
			hasValue.LogicalId(),
			value,
			awscdk.Aws_NO_VALUE(),
		),
		nil,
	)
}
