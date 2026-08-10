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
  readonly soulBindingIntegrationBearerArn?: string;
}

export interface LesserBodyDeployTemplateStackProps extends cdk.StackProps {
  readonly serviceVersion: string;
  readonly stage: string;
}

interface LesserBodyRuntimeProps {
  readonly appName: string;
  readonly stage: string;
  readonly code: lambda.Code;
  readonly instanceCode: lambda.Code;
  readonly serviceVersion?: string;
  readonly publicEndpoint: string;
  readonly instancePublicEndpoint: string;
  readonly lesserApiBaseUrl?: string;
  readonly allowedOrigins: string;
  readonly jwtSecretArnParamPath?: string;
  readonly jwtSecretKeyParamPath?: string;
  readonly lesserTableParamPath?: string;
  readonly lesserHostInstanceKeyArn?: string;
  readonly soulBindingIntegrationBearerArn?: string;
}

const MCP_SESSION_TABLE_LOGICAL_ID = "McpServerSessionTable469EA0FB";
const MCP_STREAM_TABLE_LOGICAL_ID = "McpServerStreamTableC6A2DC7E";
const MCP_TASK_TABLE_LOGICAL_ID = "McpServerTaskTable72DDFBBB";
const MCP_SESSION_TTL_MINUTES = 1440;
const INSTANCE_CONTENT_TABLE_LOGICAL_ID = "InstanceContentTable";
const INSTANCE_REGISTRY_TABLE_LOGICAL_ID = "InstanceRegistryTable";
const INSTANCE_GRANT_TABLE_LOGICAL_ID = "InstanceGrantTable";
const INSTANCE_SESSION_TABLE_LOGICAL_ID = "InstanceSessionTable";

export class LesserBodyStack extends cdk.Stack {
  constructor(scope: Construct, id: string, props: LesserBodyStackProps) {
    super(scope, id, props);

    const appName = normalizeOrDefault(props.appName, "lesser");
    const stage = normalizeStageOrDefault(props.stage, "dev");
    const stageDomain = resolvedStageDomain(this, appName, stage, props.baseDomain ?? "");
    const exactInstanceKeyArn = props.lesserHostInstanceKeyArn?.trim() || undefined;
    const soulBindingIntegrationBearerArn = props.soulBindingIntegrationBearerArn?.trim() || undefined;

    configureLesserBodyStack(this, {
      appName,
      stage,
      code: lambda.Code.fromAsset("../dist/lesser-body.zip"),
      instanceCode: lambda.Code.fromAsset("../dist/lesser-body-instance.zip"),
      serviceVersion: "dev",
      publicEndpoint: publicMcpEndpoint(stageDomain),
      instancePublicEndpoint: publicInstanceMcpEndpoint(stageDomain),
      lesserApiBaseUrl: lesserApiBaseUrl(stageDomain),
      allowedOrigins: mcpAllowedOrigins(stageDomain),
      lesserHostInstanceKeyArn: exactInstanceKeyArn,
      soulBindingIntegrationBearerArn,
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
      allowedPattern: String.raw`^/[A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.-]+)*$`,
      constraintDescription: "Must be an absolute SSM parameter path with slash-delimited alphanumeric, period, underscore, or hyphen segments.",
      description: "Required SSM parameter path containing the shared JWT secret ARN for the target app, for example /<app>/shared/secrets/jwt-secret-arn.",
    });
    cdk.Validations.of(jwtSecretArnParamPathParam).acknowledge({
      id: "CloudFormation-Validate::W2509",
      reason: "This parameter contains an SSM path, not secret material; the resolved secret ARN is never a template parameter.",
    });
    const jwtSecretKeyParamPathParam = new cdk.CfnParameter(this, "JWTSecretKeyArnParamPath", {
      type: "String",
      allowedPattern: String.raw`^/[A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.-]+)*$`,
      constraintDescription: "Must be an absolute SSM parameter path with slash-delimited alphanumeric, period, underscore, or hyphen segments.",
      description: "Required SSM parameter path containing the shared KMS key ARN for the target app, for example /<app>/shared/kms/encryption-key-arn.",
    });
    const lesserStageDomainParamPathParam = new cdk.CfnParameter(this, "LesserStageDomainParamPath", {
      type: "String",
      allowedPattern: String.raw`^/[A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.-]+)*$`,
      constraintDescription: "Must be an absolute SSM parameter path with slash-delimited alphanumeric, period, underscore, or hyphen segments.",
      description: "Required SSM parameter path containing the Lesser stage domain for the target app and stage, for example /<app>/<stage>/lesser/exports/v1/domain.",
    });
    const lesserTableParamPathParam = new cdk.CfnParameter(this, "LesserTableNameParamPath", {
      type: "String",
      allowedPattern: String.raw`^/[A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.-]+)*$`,
      constraintDescription: "Must be an absolute SSM parameter path with slash-delimited alphanumeric, period, underscore, or hyphen segments.",
      description: "Required SSM parameter path containing the Lesser table name for the target app and stage, for example /<app>/<stage>/lesser/exports/v1/table_name.",
    });
    const lesserHostInstanceKeyArnParam = new cdk.CfnParameter(this, "LesserHostInstanceKeyARN", {
      type: "String",
      default: "",
      allowedPattern: String.raw`^$|^arn:[^:*]+:secretsmanager:[a-z0-9-]+:[0-9]{12}:secret:[A-Za-z0-9/_+=.@-]+$`,
      constraintDescription: "Must be empty or an exact AWS Secrets Manager secret ARN without wildcards.",
      description: "Optional exact Secrets Manager ARN for the managed lesser-host instance key. When provided, lesser-body injects LESSER_HOST_INSTANCE_KEY_ARN and grants direct read access to that secret.",
    });
    const soulBindingIntegrationBearerArnParam = new cdk.CfnParameter(this, "LesserSoulBindingIntegrationBearerSecretARN", {
      type: "String",
      default: "",
      allowedPattern: String.raw`^$|^arn:[^:*]+:secretsmanager:[a-z0-9-]+:[0-9]{12}:secret:[A-Za-z0-9/_+=.@-]+$`,
      constraintDescription: "Must be empty or an exact AWS Secrets Manager secret ARN without wildcards.",
      description: "Managed-deploy prerequisite: exact Secrets Manager ARN for the dedicated Body/Ptah/Ba to Lesser soul-binding integration bearer. Without it, agent_local_install_plan and agent_bind_soul both fail closed with not_configured. The empty default remains for release-template compatibility; when provided, lesser-body injects LESSER_SOUL_BINDING_INTEGRATION_BEARER_ARN on the instance MCP Lambda and grants direct read access to that secret.",
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
      instanceCode: lambda.Code.fromAsset("../dist/lesser-body-instance.zip"),
      serviceVersion,
      publicEndpoint: publicMcpEndpoint(stageDomain),
      instancePublicEndpoint: publicInstanceMcpEndpoint(stageDomain),
      lesserApiBaseUrl: lesserApiBaseUrl(stageDomain),
      allowedOrigins: mcpAllowedOrigins(stageDomain),
      jwtSecretArnParamPath: jwtSecretArnParamPathParam.valueAsString,
      jwtSecretKeyParamPath: jwtSecretKeyParamPathParam.valueAsString,
      lesserTableParamPath: lesserTableParamPathParam.valueAsString,
      lesserHostInstanceKeyArn: lesserHostInstanceKeyArnParam.valueAsString,
      soulBindingIntegrationBearerArn: soulBindingIntegrationBearerArnParam.valueAsString,
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

  const exactInstanceKeyArn = optionalNonEmptyStringValue(stack, "HasLesserHostInstanceKeyARN", props.lesserHostInstanceKeyArn);
  configureLesserHostInstanceKeyAccess(stack, handler, props, exactInstanceKeyArn);

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
    sessionTtlMinutes: MCP_SESSION_TTL_MINUTES,
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
  configureLesserTableAccess(stack, handler, tableName, true);

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

  configureInstancePlaneStack(stack, props, handler, jwtSecretArnValue, jwtSecretKeyArnValue, jwtSecret, tableName, exactInstanceKeyArn);
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

interface InstancePlaneTables {
  readonly content: dynamodb.Table;
  readonly registry: dynamodb.Table;
  readonly grant: dynamodb.Table;
  readonly session: dynamodb.Table;
}

function configureInstancePlaneStack(
  stack: cdk.Stack,
  props: LesserBodyRuntimeProps,
  kaHandler: lambda.Function,
  jwtSecretArnValue: string,
  jwtSecretKeyArnValue: string,
  jwtSecret: secretsmanager.ISecret,
  lesserTableName: string,
  exactInstanceKeyArn: string | undefined,
): void {
  const tables = createInstancePlaneTables(stack, props.appName, props.stage);
  kaHandler.addEnvironment("INSTANCE_ACCOUNT_ID", props.appName);
  kaHandler.addEnvironment("INSTANCE_CONTENT_TABLE", tables.content.tableName);
  kaHandler.addEnvironment("INSTANCE_REGISTRY_TABLE", tables.registry.tableName);
  kaHandler.addToRolePolicy(new iam.PolicyStatement({
    actions: [
      "dynamodb:GetItem",
      "dynamodb:PutItem",
      "dynamodb:Query",
      "dynamodb:TransactWriteItems",
      "dynamodb:UpdateItem",
    ],
    resources: [tables.content.tableArn, tables.registry.tableArn],
  }));
  const handler = new lambda.Function(stack, "InstanceMcpHandler", {
    runtime: lambda.Runtime.PROVIDED_AL2023,
    architecture: lambda.Architecture.ARM_64,
    // provided.al2023 zip functions still enter through the packaged bootstrap;
    // this handler label keeps managed-template checks able to distinguish Ka.
    handler: "instance",
    code: props.instanceCode,
    functionName: cdk.Fn.join("-", [props.appName, props.stage, "lesser-body", "instance-mcp"]),
    memorySize: 1024,
    timeout: cdk.Duration.seconds(30),
    tracing: lambda.Tracing.ACTIVE,
    environment: {
      SERVICE_VERSION: props.serviceVersion?.trim() || "dev",
      JWT_SECRET_ARN: jwtSecretArnValue,
      INSTANCE_MCP_ENDPOINT: props.instancePublicEndpoint,
      INSTANCE_ACCOUNT_ID: props.appName,
      INSTANCE_CONTENT_TABLE: tables.content.tableName,
      INSTANCE_REGISTRY_TABLE: tables.registry.tableName,
      INSTANCE_GRANT_TABLE: tables.grant.tableName,
      INSTANCE_SESSION_TABLE: tables.session.tableName,
    },
  });
  handler.addEnvironment("MCP_SESSION_TTL_MINUTES", String(MCP_SESSION_TTL_MINUTES));

  jwtSecret.grantRead(handler);
  handler.addToRolePolicy(new iam.PolicyStatement({
    actions: ["kms:Decrypt", "kms:DescribeKey"],
    resources: [jwtSecretKeyArnValue],
  }));
  if (props.lesserApiBaseUrl?.trim()) {
    // This is a deploy-configured fallback only. Runtime request Host headers
    // remain untrusted and cannot replace the managed TRUST_CONFIG path.
    handler.addEnvironment("LESSER_API_BASE_URL", props.lesserApiBaseUrl);
  }
  configureLesserHostInstanceKeyAccess(stack, handler, props, exactInstanceKeyArn);
  configureSoulBindingIntegrationBearerAccess(stack, handler, props.soulBindingIntegrationBearerArn);
  configureLesserTableAccess(stack, handler, lesserTableName, false);
  handler.addToRolePolicy(new iam.PolicyStatement({
    actions: [
      "dynamodb:BatchGetItem",
      "dynamodb:BatchWriteItem",
      "dynamodb:DeleteItem",
      "dynamodb:DescribeTable",
      "dynamodb:GetItem",
      "dynamodb:PutItem",
      "dynamodb:Query",
      "dynamodb:TransactWriteItems",
      "dynamodb:UpdateItem",
    ],
    resources: [
      tables.content.tableArn,
      tables.registry.tableArn,
      tables.grant.tableArn,
      tables.session.tableArn,
    ],
  }));

  const instanceMcpLambdaArnParam = new ssm.CfnParameter(stack, "InstanceMcpLambdaArnParam", {
    name: ssmParamName(props.appName, props.stage, "lesser-body", "exports", "v1", "instance_mcp_lambda_arn"),
    type: "String",
    value: handler.functionArn,
  });
  instanceMcpLambdaArnParam.overrideLogicalId("InstanceMcpLambdaArnParam");

  const instanceMcpEndpointParam = new ssm.CfnParameter(stack, "InstanceMcpEndpointParam", {
    name: ssmParamName(props.appName, props.stage, "lesser-body", "exports", "v1", "instance_mcp_endpoint_url"),
    type: "String",
    value: props.instancePublicEndpoint,
  });
  instanceMcpEndpointParam.overrideLogicalId("InstanceMcpEndpointParam");

  const instanceContentTableParam = new ssm.CfnParameter(stack, "InstanceContentTableParam", {
    name: ssmParamName(props.appName, props.stage, "lesser-body", "exports", "v1", "instance_content_table_name"),
    type: "String",
    value: tables.content.tableName,
  });
  instanceContentTableParam.overrideLogicalId("InstanceContentTableParam");

  const instanceRegistryTableParam = new ssm.CfnParameter(stack, "InstanceRegistryTableParam", {
    name: ssmParamName(props.appName, props.stage, "lesser-body", "exports", "v1", "instance_registry_table_name"),
    type: "String",
    value: tables.registry.tableName,
  });
  instanceRegistryTableParam.overrideLogicalId("InstanceRegistryTableParam");

  const instanceGrantTableParam = new ssm.CfnParameter(stack, "InstanceGrantTableParam", {
    name: ssmParamName(props.appName, props.stage, "lesser-body", "exports", "v1", "instance_grant_table_name"),
    type: "String",
    value: tables.grant.tableName,
  });
  instanceGrantTableParam.overrideLogicalId("InstanceGrantTableParam");

  const instanceSessionTableParam = new ssm.CfnParameter(stack, "InstanceSessionTableParam", {
    name: ssmParamName(props.appName, props.stage, "lesser-body", "exports", "v1", "instance_session_table_name"),
    type: "String",
    value: tables.session.tableName,
  });
  instanceSessionTableParam.overrideLogicalId("InstanceSessionTableParam");
}

function configureLesserHostInstanceKeyAccess(
  stack: cdk.Stack,
  handler: lambda.Function,
  props: LesserBodyRuntimeProps,
  exactInstanceKeyArn: string | undefined,
): void {
  if (props.lesserHostInstanceKeyArn !== undefined) {
    handler.addEnvironment("LESSER_HOST_INSTANCE_KEY_ARN", props.lesserHostInstanceKeyArn);
  }

  const instanceKeySecretResources = [
    legacyInstanceKeySecretArnPattern(stack, props.appName),
    managedLesserHostInstanceKeySecretArnPattern(stack, props.stage, props.appName),
  ];
  if (exactInstanceKeyArn !== undefined) {
    instanceKeySecretResources.push(exactInstanceKeyArn);
  }

  handler.addToRolePolicy(new iam.PolicyStatement({
    actions: ["secretsmanager:GetSecretValue", "secretsmanager:DescribeSecret"],
    resources: instanceKeySecretResources,
  }));
}

function configureSoulBindingIntegrationBearerAccess(
  stack: cdk.Stack,
  handler: lambda.Function,
  soulBindingIntegrationBearerArn: string | undefined,
): void {
  if (soulBindingIntegrationBearerArn === undefined) {
    return;
  }

  const hasSoulBindingIntegrationBearerSecretARN = new cdk.CfnCondition(stack, "HasLesserSoulBindingIntegrationBearerSecretARN", {
    expression: cdk.Fn.conditionNot(cdk.Fn.conditionEquals(soulBindingIntegrationBearerArn, "")),
  });
  handler.addEnvironment("LESSER_SOUL_BINDING_INTEGRATION_BEARER_ARN", cdk.Token.asString(cdk.Fn.conditionIf(
    hasSoulBindingIntegrationBearerSecretARN.logicalId,
    soulBindingIntegrationBearerArn,
    cdk.Aws.NO_VALUE,
  )));

  const policy = new iam.Policy(stack, "SoulBindingIntegrationBearerSecretReadPolicy", {
    statements: [
      new iam.PolicyStatement({
        actions: ["secretsmanager:GetSecretValue", "secretsmanager:DescribeSecret"],
        resources: [soulBindingIntegrationBearerArn],
      }),
    ],
  });
  policy.attachToRole(handler.role!);

  const cfnPolicy = policy.node.defaultChild;
  if (!(cfnPolicy instanceof iam.CfnPolicy)) {
    throw new Error("soul-binding integration bearer secret policy is not a CloudFormation policy");
  }
  cfnPolicy.cfnOptions.condition = hasSoulBindingIntegrationBearerSecretARN;
}

function configureLesserTableAccess(
  stack: cdk.Stack,
  handler: lambda.Function,
  tableName: string,
  includeMemoryWrite: boolean,
): void {
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
  const lesserTableReadKeys = includeMemoryWrite
    ? ["LBMEMORY#*", "SOUL_BODY_BINDING_USERNAME#*", "INSTANCE#CONFIG"]
    : ["SOUL_BODY_BINDING_USERNAME#*", "INSTANCE#CONFIG"];
  handler.addToRolePolicy(new iam.PolicyStatement({
    actions: ["dynamodb:Query", "dynamodb:GetItem"],
    resources: [tableArn],
    conditions: dynamodbLeadingKeysCondition(...lesserTableReadKeys),
  }));
  if (includeMemoryWrite) {
    handler.addToRolePolicy(new iam.PolicyStatement({
      actions: ["dynamodb:PutItem"],
      resources: [tableArn],
      conditions: dynamodbLeadingKeysCondition("LBMEMORY#*"),
    }));
  }
}

function createInstancePlaneTables(stack: cdk.Stack, appName: string, stage: string): InstancePlaneTables {
  const content = newInstancePlaneTable(stack, "InstanceContentTable", appName, stage, "content", INSTANCE_CONTENT_TABLE_LOGICAL_ID);
  const registry = newInstancePlaneTable(stack, "InstanceRegistryTable", appName, stage, "registry", INSTANCE_REGISTRY_TABLE_LOGICAL_ID);
  const grant = newInstancePlaneTable(stack, "InstanceGrantTable", appName, stage, "grants", INSTANCE_GRANT_TABLE_LOGICAL_ID);
  const session = newInstancePlaneTable(stack, "InstanceSessionTable", appName, stage, "sessions", INSTANCE_SESSION_TABLE_LOGICAL_ID, "expiresAt");
  return { content, registry, grant, session };
}

function newInstancePlaneTable(
  stack: cdk.Stack,
  id: string,
  appName: string,
  stage: string,
  suffix: string,
  logicalId: string,
  ttlAttribute?: string,
): dynamodb.Table {
  const table = new dynamodb.Table(stack, id, {
    tableName: cdk.Fn.join("-", [appName, stage, "instance", suffix]),
    partitionKey: { name: "pk", type: dynamodb.AttributeType.STRING },
    sortKey: { name: "sk", type: dynamodb.AttributeType.STRING },
    billingMode: dynamodb.BillingMode.PAY_PER_REQUEST,
    encryption: dynamodb.TableEncryption.AWS_MANAGED,
    pointInTimeRecoverySpecification: { pointInTimeRecoveryEnabled: true },
    removalPolicy: cdk.RemovalPolicy.RETAIN,
    timeToLiveAttribute: ttlAttribute,
  });
  overrideRemoteMcpTableLogicalId(table, logicalId);
  return table;
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

function publicInstanceMcpEndpoint(stageDomain: string): string {
  return cdk.Fn.join("", ["https://api.", stageDomain, "/instance/{surface}/mcp"]);
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
