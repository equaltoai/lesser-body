package stacks

import (
	"fmt"
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

	fnName := fmt.Sprintf("%s-%s-lesser-body-mcp", appName, stage)
	handler := awslambda.NewFunction(stack, jsii.String("McpHandler"), &awslambda.FunctionProps{
		Runtime:      awslambda.Runtime_PROVIDED_AL2023(),
		Architecture: awslambda.Architecture_ARM_64(),
		Handler:      jsii.String("bootstrap"),
		Code:         awslambda.Code_FromAsset(jsii.String("../dist/lesser-body.zip"), nil),
		FunctionName: jsii.String(fnName),
		MemorySize:   jsii.Number(1024),
		Timeout:      awscdk.Duration_Seconds(jsii.Number(30)),
		Tracing:      awslambda.Tracing_ACTIVE,
		Environment: &map[string]*string{
			"SERVICE_VERSION": jsii.String("dev"),
		},
	})

	// Share Lesser auth configuration via well-known SSM parameters (no CFN exports/imports).
	jwtSecretArnParamName := fmt.Sprintf("/%s/shared/secrets/jwt-secret-arn", appName)
	jwtSecretArnParam := awsssm.StringParameter_FromStringParameterName(
		stack,
		jsii.String("JWTSecretArnParamLookup"),
		jsii.String(jwtSecretArnParamName),
	)
	jwtSecretKeyArnParam := awsssm.StringParameter_FromStringParameterName(
		stack,
		jsii.String("JWTSecretKeyArnParamLookup"),
		jsii.String(fmt.Sprintf("/%s/shared/kms/encryption-key-arn", appName)),
	)
	handler.AddEnvironment(jsii.String("JWT_SECRET_ARN"), jwtSecretArnParam.StringValue(), nil)
	jwtSecret := awssecretsmanager.Secret_FromSecretCompleteArn(
		stack,
		jsii.String("ImportedJWTSecret"),
		jwtSecretArnParam.StringValue(),
	)
	jwtSecret.GrantRead(handler, nil)
	handler.AddToRolePolicy(awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
		Actions: &[]*string{
			jsii.String("kms:Decrypt"),
			jsii.String("kms:DescribeKey"),
		},
		Resources: &[]*string{
			jwtSecretKeyArnParam.StringValue(),
		},
	}))

	// Managed lesser-host stores the instance key in the instance account under
	// a stable secret name: <app>/instance-key. The runtime resolves the exact
	// ARN from TRUST_CONFIG, so the Lambda only needs permission to read the
	// matching secret resource in this account.
	handler.AddToRolePolicy(awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
		Actions: &[]*string{
			jsii.String("secretsmanager:GetSecretValue"),
			jsii.String("secretsmanager:DescribeSecret"),
		},
		Resources: &[]*string{
			stack.FormatArn(&awscdk.ArnComponents{
				Service:      jsii.String("secretsmanager"),
				Resource:     jsii.String("secret"),
				ArnFormat:    awscdk.ArnFormat_COLON_RESOURCE_NAME,
				ResourceName: jsii.String(fmt.Sprintf("%s/instance-key*", appName)),
			}),
		},
	}))

	mcpProps := &apptheorycdk.AppTheoryRemoteMcpServerProps{
		Handler: handler,
		ApiName: jsii.String(fmt.Sprintf("%s-%s-mcp", appName, stage)),
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
		SessionTableName:   jsii.String(fmt.Sprintf("%s-%s-mcp-sessions", appName, stage)),
		SessionTtlMinutes:  jsii.Number(60),
		Stage: &apptheorycdk.AppTheoryRestApiRouterStageOptions{
			StageName:          jsii.String(stage),
			AccessLogging:      true,
			AccessLogRetention: awslogs.RetentionDays_ONE_WEEK,
		},
	}

	// Allow the MCP runtime to read required cross-stack values via SSM (no CFN exports/imports).
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
				ResourceName: jsii.String(fmt.Sprintf("%s/%s/lesser/exports/v1/*", appName, stage)),
			}),
			stack.FormatArn(&awscdk.ArnComponents{
				Service:      jsii.String("ssm"),
				Resource:     jsii.String("parameter"),
				ResourceName: jsii.String(fmt.Sprintf("%s/%s/lesser-soul/exports/v1/*", appName, stage)),
			}),
		},
	}))

	server := apptheorycdk.NewAppTheoryRemoteMcpServer(stack, jsii.String("McpServer"), mcpProps)

	// Expose /.well-known/mcp.json from the same handler (non-streaming).
	server.Router().AddLambdaIntegration(
		jsii.String("/.well-known/mcp.json"),
		&[]*string{jsii.String("GET")},
		handler,
		&apptheorycdk.AppTheoryRestApiRouterIntegrationOptions{},
	)
	server.Router().AddLambdaIntegration(
		jsii.String("/.well-known/oauth-protected-resource"),
		&[]*string{jsii.String("GET")},
		handler,
		&apptheorycdk.AppTheoryRestApiRouterIntegrationOptions{},
	)

	stageDomain := resolvedStageDomain(stack, appName, stage, props.BaseDomain)
	publicEndpoint := publicMcpEndpoint(stageDomain)

	// Ensure the runtime sees the correct endpoint and TTL minutes (older CDK bindings may not set these).
	handler.AddEnvironment(jsii.String("MCP_ENDPOINT"), publicEndpoint, nil)
	handler.AddEnvironment(jsii.String("MCP_ALLOWED_ORIGINS"), mcpAllowedOrigins(stageDomain), nil)
	handler.AddEnvironment(jsii.String("MCP_SESSION_TTL_MINUTES"), jsii.String("60"), nil)

	// Memory tools use Lesser's main table. Import the table name from SSM and grant minimal DynamoDB access.
	lesserTableParamName := fmt.Sprintf("/%s/%s/lesser/exports/v1/table_name", appName, stage)
	lesserTableParam := awsssm.StringParameter_FromStringParameterName(
		stack,
		jsii.String("LesserTableNameParamLookup"),
		jsii.String(lesserTableParamName),
	)
	handler.AddEnvironment(jsii.String("LESSER_TABLE_NAME"), lesserTableParam.StringValue(), nil)
	tableName := lesserTableParam.StringValue()
	tableArn := stack.FormatArn(&awscdk.ArnComponents{
		Service:      jsii.String("dynamodb"),
		Resource:     jsii.String("table"),
		ResourceName: tableName,
	})
	indexArn := stack.FormatArn(&awscdk.ArnComponents{
		Service:  jsii.String("dynamodb"),
		Resource: jsii.String("table"),
		ResourceName: awscdk.Fn_Join(jsii.String("/"), &[]*string{
			tableName,
			jsii.String("index/*"),
		}),
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

	paramPrefix := fmt.Sprintf("/%s/%s/lesser-body/exports/v1", appName, stage)
	awsssm.NewStringParameter(stack, jsii.String("McpLambdaArnParam"), &awsssm.StringParameterProps{
		ParameterName: jsii.String(fmt.Sprintf("%s/mcp_lambda_arn", paramPrefix)),
		StringValue:   handler.FunctionArn(),
	})
	awsssm.NewStringParameter(stack, jsii.String("McpEndpointParam"), &awsssm.StringParameterProps{
		ParameterName: jsii.String(fmt.Sprintf("%s/mcp_endpoint_url", paramPrefix)),
		StringValue:   publicEndpoint,
	})
	if server.SessionTable() != nil {
		awsssm.NewStringParameter(stack, jsii.String("McpSessionTableParam"), &awsssm.StringParameterProps{
			ParameterName: jsii.String(fmt.Sprintf("%s/mcp_session_table_name", paramPrefix)),
			StringValue:   server.SessionTable().TableName(),
		})
	}

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
		jsii.String("/mcp"),
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
