import * as cdk from "aws-cdk-lib";
import * as dynamodb from "aws-cdk-lib/aws-dynamodb";
import * as iam from "aws-cdk-lib/aws-iam";
import * as lambda from "aws-cdk-lib/aws-lambda";
import * as logs from "aws-cdk-lib/aws-logs";
import * as secretsmanager from "aws-cdk-lib/aws-secretsmanager";
import * as ssm from "aws-cdk-lib/aws-ssm";
import { Construct } from "constructs";
import {
  AppTheoryRemoteMcpServer,
  type AppTheoryRestApiRouterCorsOptions,
  type AppTheoryRestApiRouterStageOptions,
} from "@theory-cloud/apptheory-cdk";

export interface LesserBodyStackProps extends cdk.StackProps {
  readonly appName: string;
  readonly stage: string;
  readonly baseDomain?: string;
  readonly lesserHostInstanceKeyArn?: string;
}

export interface LesserBodyDeployTemplateStackProps extends cdk.StackProps {
  readonly serviceVersion: string;
  readonly stage: string;
}

interface LesserBodyRuntimeProps {
  readonly appName: string;
  readonly stage: string;
  readonly code: lambda.Code;
  readonly serviceVersion?: string;
  readonly publicEndpoint: string;
  readonly lesserApiBaseUrl?: string;
  readonly allowedOrigins: string;
  readonly jwtSecretArnParamPath?: string;
  readonly jwtSecretKeyParamPath?: string;
  readonly lesserTableParamPath?: string;
  readonly lesserHostInstanceKeyArn?: string;
}

const MCP_SESSION_TABLE_LOGICAL_ID = "McpServerSessionTable469EA0FB";
const MCP_STREAM_TABLE_LOGICAL_ID = "McpServerStreamTableC6A2DC7E";
const MCP_TASK_TABLE_LOGICAL_ID = "McpServerTaskTable72DDFBBB";

export class LesserBodyStack extends cdk.Stack {
  constructor(scope: Construct, id: string, props: LesserBodyStackProps) {
    super(scope, id, props);

    const appName = normalizeOrDefault(props.appName, "lesser");
    const stage = normalizeStageOrDefault(props.stage, "dev");
    const stageDomain = resolvedStageDomain(this, appName, stage, props.baseDomain ?? "");
    const exactInstanceKeyArn = props.lesserHostInstanceKeyArn?.trim() || undefined;

    configureLesserBodyStack(this, {
      appName,
      stage,
      code: lambda.Code.fromAsset("../dist/lesser-body.zip"),
      serviceVersion: "dev",
      publicEndpoint: publicMcpEndpoint(stageDomain),
      lesserApiBaseUrl: lesserApiBaseUrl(stageDomain),
      allowedOrigins: mcpAllowedOrigins(stageDomain),
      lesserHostInstanceKeyArn: exactInstanceKeyArn,
    });
  }
}

export class LesserBodyDeployTemplateStack extends cdk.Stack {
  constructor(scope: Construct, id: string, props: LesserBodyDeployTemplateStackProps) {
    super(scope, id, props);

    const serviceVersion = props.serviceVersion.trim() || "dev";
    const stage = normalizeRequiredStage(props.stage);

    const appNameParam = new cdk.CfnParameter(this, "AppName", {
      type: "String",
      default: "lesser",
      description: "Lesser app slug used in stack naming and SSM paths.",
    });
    const baseDomainParam = new cdk.CfnParameter(this, "BaseDomain", {
      type: "String",
      default: "",
      description: "Optional base domain override. Leave empty to use /<app>/<stage>/lesser/exports/v1/domain from SSM.",
    });
    const codeBucketParam = new cdk.CfnParameter(this, "LesserBodyCodeBucketName", {
      type: "String",
      description: "S3 bucket containing the lesser-body Lambda zip release asset.",
    });
    const codeKeyParam = new cdk.CfnParameter(this, "LesserBodyCodeObjectKey", {
      type: "String",
      description: "S3 object key for the lesser-body Lambda zip release asset.",
    });
    const jwtSecretArnParamPathParam = new cdk.CfnParameter(this, "JWTSecretArnParamPath", {
      type: "String",
      description: "Required SSM parameter path containing the shared JWT secret ARN for the target app, for example /<app>/shared/secrets/jwt-secret-arn.",
    });
    const jwtSecretKeyParamPathParam = new cdk.CfnParameter(this, "JWTSecretKeyArnParamPath", {
      type: "String",
      description: "Required SSM parameter path containing the shared KMS key ARN for the target app, for example /<app>/shared/kms/encryption-key-arn.",
    });
    const lesserStageDomainParamPathParam = new cdk.CfnParameter(this, "LesserStageDomainParamPath", {
      type: "String",
      description: "Required SSM parameter path containing the Lesser stage domain for the target app and stage, for example /<app>/<stage>/lesser/exports/v1/domain.",
    });
    const lesserTableParamPathParam = new cdk.CfnParameter(this, "LesserTableNameParamPath", {
      type: "String",
      description: "Required SSM parameter path containing the Lesser table name for the target app and stage, for example /<app>/<stage>/lesser/exports/v1/table_name.",
    });
    const lesserHostInstanceKeyArnParam = new cdk.CfnParameter(this, "LesserHostInstanceKeyARN", {
      type: "String",
      default: "",
      allowedPattern: String.raw`^$|^arn:[^:*]+:secretsmanager:[a-z0-9-]+:[0-9]{12}:secret:[A-Za-z0-9/_+=.@-]+$`,
      constraintDescription: "Must be empty or an exact AWS Secrets Manager secret ARN without wildcards.",
      description: "Optional exact Secrets Manager ARN for the managed lesser-host instance key. When provided, lesser-body injects LESSER_HOST_INSTANCE_KEY_ARN and grants direct read access to that secret.",
    });

    const stageDomain = resolvedStageDomainFromDeployInputs(
      this,
      stage,
      baseDomainParam.valueAsString,
      lesserStageDomainParamPathParam.valueAsString,
    );

    configureLesserBodyStack(this, {
      appName: appNameParam.valueAsString,
      stage,
      code: lambda.Code.fromCfnParameters({
        bucketNameParam: codeBucketParam,
        objectKeyParam: codeKeyParam,
      }),
      serviceVersion,
      publicEndpoint: publicMcpEndpoint(stageDomain),
      lesserApiBaseUrl: lesserApiBaseUrl(stageDomain),
      allowedOrigins: mcpAllowedOrigins(stageDomain),
      jwtSecretArnParamPath: jwtSecretArnParamPathParam.valueAsString,
      jwtSecretKeyParamPath: jwtSecretKeyParamPathParam.valueAsString,
      lesserTableParamPath: lesserTableParamPathParam.valueAsString,
      lesserHostInstanceKeyArn: lesserHostInstanceKeyArnParam.valueAsString,
    });
  }
}

export function configureLesserBodyStack(stack: cdk.Stack, props: LesserBodyRuntimeProps): void {
  const handler = new lambda.Function(stack, "McpHandler", {
    runtime: lambda.Runtime.PROVIDED_AL2023,
    architecture: lambda.Architecture.ARM_64,
    handler: "bootstrap",
    code: props.code,
    functionName: cdk.Fn.join("-", [props.appName, props.stage, "lesser-body", "mcp"]),
    memorySize: 1024,
    timeout: cdk.Duration.seconds(30),
    tracing: lambda.Tracing.ACTIVE,
    environment: {
      SERVICE_VERSION: props.serviceVersion?.trim() || "dev",
    },
  });

  const jwtSecretArnParamPath = props.jwtSecretArnParamPath || ssmParamName(props.appName, "shared", "secrets", "jwt-secret-arn");
  const jwtSecretKeyParamPath = props.jwtSecretKeyParamPath || ssmParamName(props.appName, "shared", "kms", "encryption-key-arn");
  const jwtSecretArnValue = lookupStringParameterValue(stack, "JWTSecretArnParamLookup", props.jwtSecretArnParamPath, jwtSecretArnParamPath);
  const jwtSecretKeyArnValue = lookupStringParameterValue(stack, "JWTSecretKeyArnParamLookup", props.jwtSecretKeyParamPath, jwtSecretKeyParamPath);

  handler.addEnvironment("JWT_SECRET_ARN", jwtSecretArnValue);
  const jwtSecret = secretsmanager.Secret.fromSecretCompleteArn(stack, "ImportedJWTSecret", jwtSecretArnValue);
  jwtSecret.grantRead(handler);
  handler.addToRolePolicy(new iam.PolicyStatement({
    actions: ["kms:Decrypt", "kms:DescribeKey"],
    resources: [jwtSecretKeyArnValue],
  }));

  if (props.lesserHostInstanceKeyArn !== undefined) {
    handler.addEnvironment("LESSER_HOST_INSTANCE_KEY_ARN", props.lesserHostInstanceKeyArn);
  }

  const instanceKeySecretResources = [
    legacyInstanceKeySecretArnPattern(stack, props.appName),
    managedLesserHostInstanceKeySecretArnPattern(stack, props.stage, props.appName),
  ];
  const exactSecretArn = optionalNonEmptyStringValue(stack, "HasLesserHostInstanceKeyARN", props.lesserHostInstanceKeyArn);
  if (exactSecretArn !== undefined) {
    instanceKeySecretResources.push(exactSecretArn);
  }

  handler.addToRolePolicy(new iam.PolicyStatement({
    actions: ["secretsmanager:GetSecretValue", "secretsmanager:DescribeSecret"],
    resources: instanceKeySecretResources,
  }));

  const mcpProps = {
    handler,
    apiName: cdk.Fn.join("-", [props.appName, props.stage, "mcp"]),
    actorPath: true,
    enableWellKnownMcpDiscovery: true,
    cors: {
      allowOrigins: ["*"],
      allowHeaders: [
        "authorization",
        "content-type",
        "mcp-protocol-version",
        "mcp-session-id",
        "last-event-id",
        "lesser-x402-grant",
        "x-lesser-x402-grant",
        "lesser-x402-grant-id",
        "x-lesser-x402-grant-id",
        "lesser-x402-capability",
        "x-lesser-x402-capability",
        "payment-signature",
        "x-payment",
      ],
      allowMethods: ["GET", "POST", "DELETE", "OPTIONS"],
    } satisfies AppTheoryRestApiRouterCorsOptions,
    enableSessionTable: true,
    sessionTableName: cdk.Fn.join("-", [props.appName, props.stage, "mcp", "sessions"]),
    sessionTtlMinutes: 60,
    enableStreamTable: true,
    streamTableName: cdk.Fn.join("-", [props.appName, props.stage, "mcp", "streams", "v2"]),
    streamTtlMinutes: 60,
    enableTaskTable: true,
    taskTableName: cdk.Fn.join("-", [props.appName, props.stage, "mcp", "tasks"]),
    taskTtlMinutes: 10,
    stage: {
      stageName: props.stage,
      accessLogging: true,
      accessLogRetention: logs.RetentionDays.ONE_WEEK,
    } satisfies AppTheoryRestApiRouterStageOptions,
  };

  handler.addToRolePolicy(new iam.PolicyStatement({
    actions: ["ssm:GetParameter", "ssm:GetParameters", "ssm:GetParametersByPath"],
    resources: [
      stack.formatArn({
        service: "ssm",
        resource: "parameter",
        resourceName: ssmResourceName(props.appName, props.stage, "lesser", "exports", "v1", "*"),
      }),
      stack.formatArn({
        service: "ssm",
        resource: "parameter",
        resourceName: ssmResourceName(props.appName, props.stage, "lesser-soul", "exports", "v1", "*"),
      }),
    ],
  }));

  const server = new AppTheoryRemoteMcpServer(stack, "McpServer", mcpProps);
  overrideRemoteMcpTableLogicalId(server.sessionTable, MCP_SESSION_TABLE_LOGICAL_ID);
  overrideRemoteMcpTableLogicalId(server.streamTable, MCP_STREAM_TABLE_LOGICAL_ID);
  overrideRemoteMcpTableLogicalId(server.taskTable, MCP_TASK_TABLE_LOGICAL_ID);

  handler.addEnvironment("MCP_ENDPOINT", props.publicEndpoint);
  if (props.lesserApiBaseUrl?.trim()) {
    handler.addEnvironment("LESSER_API_BASE_URL", props.lesserApiBaseUrl);
  }
  handler.addEnvironment("MCP_ALLOWED_ORIGINS", props.allowedOrigins);

  const lesserTableParamPath = props.lesserTableParamPath || ssmParamName(props.appName, props.stage, "lesser", "exports", "v1", "table_name");
  const tableName = lookupStringParameterValue(stack, "LesserTableNameParamLookup", props.lesserTableParamPath, lesserTableParamPath);
  handler.addEnvironment("LESSER_TABLE_NAME", tableName);
  const tableArn = stack.formatArn({
    service: "dynamodb",
    resource: "table",
    resourceName: tableName,
  });
  handler.addToRolePolicy(new iam.PolicyStatement({
    actions: ["dynamodb:DescribeTable"],
    resources: [tableArn],
  }));
  handler.addToRolePolicy(new iam.PolicyStatement({
    actions: ["dynamodb:Query", "dynamodb:GetItem"],
    resources: [tableArn],
    conditions: dynamodbLeadingKeysCondition("LBMEMORY#*", "SOUL_BODY_BINDING_USERNAME#*", "INSTANCE#CONFIG"),
  }));
  handler.addToRolePolicy(new iam.PolicyStatement({
    actions: ["dynamodb:PutItem"],
    resources: [tableArn],
    conditions: dynamodbLeadingKeysCondition("LBMEMORY#*"),
  }));

  const mcpLambdaArnParam = new ssm.CfnParameter(stack, "McpLambdaArnParam", {
    name: ssmParamName(props.appName, props.stage, "lesser-body", "exports", "v1", "mcp_lambda_arn"),
    type: "String",
    value: handler.functionArn,
  });
  mcpLambdaArnParam.overrideLogicalId("McpLambdaArnParamE9C053F0");

  const mcpEndpointParam = new ssm.CfnParameter(stack, "McpEndpointParam", {
    name: ssmParamName(props.appName, props.stage, "lesser-body", "exports", "v1", "mcp_endpoint_url"),
    type: "String",
    value: props.publicEndpoint,
  });
  mcpEndpointParam.overrideLogicalId("McpEndpointParam71B07820");

  if (server.sessionTable) {
    const mcpSessionTableParam = new ssm.CfnParameter(stack, "McpSessionTableParam", {
      name: ssmParamName(props.appName, props.stage, "lesser-body", "exports", "v1", "mcp_session_table_name"),
      type: "String",
      value: server.sessionTable.tableName,
    });
    mcpSessionTableParam.overrideLogicalId("McpSessionTableParam11A03692");
  }
  if (server.streamTable) {
    const mcpStreamTableParam = new ssm.CfnParameter(stack, "McpStreamTableParam", {
      name: ssmParamName(props.appName, props.stage, "lesser-body", "exports", "v1", "mcp_stream_table_name"),
      type: "String",
      value: server.streamTable.tableName,
    });
    mcpStreamTableParam.overrideLogicalId("McpStreamTableParam604E9EFA");
  }
}

function overrideRemoteMcpTableLogicalId(table: dynamodb.ITable | undefined, logicalId: string): void {
  if (!table) {
    return;
  }
  const defaultChild = table.node.defaultChild;
  if (!defaultChild) {
    throw new Error("remote MCP table is missing a default child");
  }
  if (!(defaultChild instanceof cdk.CfnResource)) {
    throw new Error("remote MCP table default child is not a CloudFormation resource");
  }
  defaultChild.overrideLogicalId(logicalId);
}

function ssmParamName(...parts: string[]): string {
  return cdk.Fn.join("/", ["", ...parts]);
}

function ssmResourceName(...parts: string[]): string {
  return cdk.Fn.join("/", parts);
}

function importStringParameter(scope: Construct, id: string, parameterName: string): ssm.IStringParameter {
  return ssm.StringParameter.fromStringParameterAttributes(scope, id, {
    parameterName,
    simpleName: false,
  });
}

function lookupStringParameterValue(stack: cdk.Stack, id: string, explicitPath: string | undefined, fallbackPath: string): string {
  if (explicitPath?.trim()) {
    return cdk.Token.asString(new cdk.CfnDynamicReference(cdk.CfnDynamicReferenceService.SSM, explicitPath));
  }
  return importStringParameter(stack, id, fallbackPath).stringValue;
}

function legacyInstanceKeySecretArnPattern(stack: cdk.Stack, appName: string): string {
  return stack.formatArn({
    service: "secretsmanager",
    resource: "secret",
    arnFormat: cdk.ArnFormat.COLON_RESOURCE_NAME,
    resourceName: cdk.Fn.join("/", [appName, "instance-key*"]),
  });
}

function managedLesserHostInstanceKeySecretArnPattern(stack: cdk.Stack, stage: string, appName: string): string {
  return stack.formatArn({
    service: "secretsmanager",
    resource: "secret",
    arnFormat: cdk.ArnFormat.COLON_RESOURCE_NAME,
    resourceName: cdk.Fn.join("/", ["lesser-host", lesserHostControlPlaneStage(stage), "instances", appName, "instance-key*"]),
  });
}

function lesserHostControlPlaneStage(stage: string): string {
  switch (stage.trim().toLowerCase()) {
    case "live":
      return "live";
    case "staging":
      return "staging";
    default:
      return "lab";
  }
}

function optionalNonEmptyStringValue(stack: cdk.Stack, id: string, value: string | undefined): string | undefined {
  if (value === undefined) {
    return undefined;
  }
  const hasValue = new cdk.CfnCondition(stack, id, {
    expression: cdk.Fn.conditionNot(cdk.Fn.conditionEquals(value, "")),
  });
  return cdk.Token.asString(cdk.Fn.conditionIf(hasValue.logicalId, value, cdk.Aws.NO_VALUE));
}

function dynamodbLeadingKeysCondition(...keys: string[]): Record<string, unknown> {
  return {
    "ForAllValues:StringLike": {
      "dynamodb:LeadingKeys": keys.map((key) => key.trim()).filter(Boolean),
    },
  };
}

function resolvedStageDomain(stack: cdk.Stack, appName: string, stage: string, baseDomain: string): string {
  if (baseDomain.trim()) {
    return stageDomainFor(stage, baseDomain);
  }
  const paramName = `/${appName}/${stage}/lesser/exports/v1/domain`;
  return ssm.StringParameter.fromStringParameterName(stack, "LesserStageDomainParamLookup", paramName).stringValue;
}

function resolvedStageDomainFromDeployInputs(stack: cdk.Stack, stage: string, baseDomain: string, stageDomainParamPath: string): string {
  const hasBaseDomain = new cdk.CfnCondition(stack, "HasBaseDomain", {
    expression: cdk.Fn.conditionNot(cdk.Fn.conditionEquals(baseDomain, "")),
  });
  const stageDomainFromBase = stage === "live" ? baseDomain : cdk.Fn.join(".", [stage, baseDomain]);
  const domainValue = cdk.Token.asString(new cdk.CfnDynamicReference(cdk.CfnDynamicReferenceService.SSM, stageDomainParamPath));
  return cdk.Token.asString(cdk.Fn.conditionIf(hasBaseDomain.logicalId, stageDomainFromBase, domainValue));
}

function publicMcpEndpoint(stageDomain: string): string {
  return cdk.Fn.join("", ["https://api.", stageDomain, "/mcp/{actor}"]);
}

function lesserApiBaseUrl(stageDomain: string): string {
  return cdk.Fn.join("", ["https://api.", stageDomain]);
}

function mcpAllowedOrigins(stageDomain: string): string {
  return cdk.Fn.join("", [
    "https://claude.ai,https://claude.com,https://",
    stageDomain,
    ",https://app.",
    stageDomain,
    ",https://api.",
    stageDomain,
  ]);
}

function stageDomainFor(stage: string, baseDomain: string): string {
  const base = baseDomain.trim().toLowerCase().replace(/\.+$/, "");
  if (!base) {
    return "";
  }
  if (stage === "live") {
    return base;
  }
  return `${stage}.${base}`;
}

function normalizeOrDefault(value: string, fallback: string): string {
  return value.trim() || fallback;
}

function normalizeStageOrDefault(value: string, fallback: string): string {
  return (value.trim().toLowerCase() || fallback);
}

function normalizeRequiredStage(value: string): string {
  const stage = value.trim().toLowerCase();
  if (!["dev", "staging", "live"].includes(stage)) {
    throw new Error("stage must be one of dev, staging, live");
  }
  return stage;
}
