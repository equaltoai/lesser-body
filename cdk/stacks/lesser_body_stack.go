package stacks

import (
	"fmt"
	"strings"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awscertificatemanager"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsiam"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslambda"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslogs"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsroute53"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsssm"
	"github.com/aws/constructs-go/constructs/v10"
	"github.com/aws/jsii-runtime-go"
	apptheorycdk "github.com/theory-cloud/apptheory/cdk-go/apptheorycdk"
)

type LesserBodyStackProps struct {
	awscdk.StackProps
	AppName      string
	Stage        string // dev|staging|live
	BaseDomain   string // optional; enables custom domain
	HostedZoneId string // optional; speeds up hosted zone lookup
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

	mcpProps := &apptheorycdk.AppTheoryMcpServerProps{
		Handler:            handler,
		ApiName:            jsii.String(fmt.Sprintf("%s-%s-mcp", appName, stage)),
		EnableSessionTable: jsii.Bool(true),
		SessionTableName:   jsii.String(fmt.Sprintf("%s-%s-mcp-sessions", appName, stage)),
		SessionTtlMinutes:  jsii.Number(60),
		Stage: &apptheorycdk.AppTheoryMcpServerStageOptions{
			StageName:          jsii.String(stage),
			AccessLogging:      jsii.Bool(true),
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

	// Optional custom domain: mcp.<stageDomain>.
	var mcpEndpoint *string
	if props.BaseDomain != "" {
		stageDomain := stageDomainFor(stage, props.BaseDomain)
		mcpDomain := fmt.Sprintf("mcp.%s", stageDomain)

		zone := lookupHostedZone(stack, props.BaseDomain, props.HostedZoneId)
		if zone != nil {
			cert := awscertificatemanager.NewCertificate(stack, jsii.String("McpCertificate"), &awscertificatemanager.CertificateProps{
				DomainName:                 jsii.String(mcpDomain),
				Validation:                 awscertificatemanager.CertificateValidation_FromDns(zone),
				TransparencyLoggingEnabled: jsii.Bool(true),
			})
			mcpProps.Domain = &apptheorycdk.AppTheoryMcpServerDomainOptions{
				DomainName:  jsii.String(mcpDomain),
				Certificate: cert,
				HostedZone:  zone,
			}
			mcpEndpoint = jsii.String(fmt.Sprintf("https://%s/mcp", mcpDomain))
		}
	}

	server := apptheorycdk.NewAppTheoryMcpServer(stack, jsii.String("McpServer"), mcpProps)

	// Prefer the custom domain endpoint when configured; otherwise include the stage path for execute-api.
	if mcpEndpoint == nil {
		mcpEndpoint = awscdk.Fn_Join(jsii.String(""), &[]*string{
			server.Api().ApiEndpoint(),
			jsii.String("/"),
			jsii.String(stage),
			jsii.String("/mcp"),
		})
	}

	// Ensure the runtime sees the correct endpoint and TTL minutes (older CDK bindings may not set these).
	handler.AddEnvironment(jsii.String("MCP_ENDPOINT"), mcpEndpoint, nil)
	handler.AddEnvironment(jsii.String("MCP_SESSION_TTL_MINUTES"), jsii.String("60"), nil)

	paramPrefix := fmt.Sprintf("/%s/%s/lesser-body/exports/v1", appName, stage)
	awsssm.NewStringParameter(stack, jsii.String("McpLambdaArnParam"), &awsssm.StringParameterProps{
		ParameterName: jsii.String(fmt.Sprintf("%s/mcp_lambda_arn", paramPrefix)),
		StringValue:   handler.FunctionArn(),
	})
	awsssm.NewStringParameter(stack, jsii.String("McpEndpointParam"), &awsssm.StringParameterProps{
		ParameterName: jsii.String(fmt.Sprintf("%s/mcp_endpoint_url", paramPrefix)),
		StringValue:   mcpEndpoint,
	})
	if server.SessionTable() != nil {
		awsssm.NewStringParameter(stack, jsii.String("McpSessionTableParam"), &awsssm.StringParameterProps{
			ParameterName: jsii.String(fmt.Sprintf("%s/mcp_session_table_name", paramPrefix)),
			StringValue:   server.SessionTable().TableName(),
		})
	}

	return &LesserBodyStack{Stack: stack}
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

func lookupHostedZone(scope constructs.Construct, zoneDomain string, zoneId string) awsroute53.IHostedZone {
	zoneName := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(zoneDomain)), ".")
	if zoneName == "" {
		return nil
	}
	if strings.TrimSpace(zoneId) != "" {
		return awsroute53.HostedZone_FromHostedZoneAttributes(scope, jsii.String("HostedZone"), &awsroute53.HostedZoneAttributes{
			HostedZoneId: jsii.String(zoneId),
			ZoneName:     jsii.String(zoneName),
		})
	}
	return awsroute53.HostedZone_FromLookup(scope, jsii.String("HostedZone"), &awsroute53.HostedZoneProviderProps{
		DomainName: jsii.String(zoneName),
	})
}
