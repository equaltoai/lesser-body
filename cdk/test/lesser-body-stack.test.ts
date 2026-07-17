import assert from "node:assert/strict";
import { mkdtempSync, writeFileSync, chmodSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

import * as cdk from "aws-cdk-lib";
import * as lambda from "aws-cdk-lib/aws-lambda";

import {
  LesserBodyDeployTemplateStack,
  configureLesserBodyStack,
} from "../lib/lesser-body-stack";

test("secret read policy includes legacy and managed instance-key patterns", () => {
  const template = synthRuntimeTemplate();
  const statements = allPolicyStatements(mustResources(template));

  assert.equal(statementResourcesContain(statements, ":secret:theory/instance-key*"), true);
  assert.equal(statementResourcesContain(statements, ":secret:lesser-host/lab/instances/theory/instance-key*"), true);
});

test("managed template supports an exact lesser-host instance key ARN", () => {
  const template = synthManagedTemplate();
  const params = mustRecord(template.Parameters, "template missing Parameters");
  const param = mustRecord(params.LesserHostInstanceKeyARN, "template missing LesserHostInstanceKeyARN parameter");

  assert.equal(param.Default, "");
  assert.equal(typeof param.AllowedPattern, "string");
  assert.match(param.AllowedPattern as string, /\^\$\|\^arn:/);
  assert.match(param.AllowedPattern as string, /secretsmanager/);

  const handler = mcpHandlerLambdaFunction(mustResources(template));
  const vars = lambdaEnvironmentVariables(handler);
  assert.match(mustJSON(vars.LESSER_HOST_INSTANCE_KEY_ARN), /LesserHostInstanceKeyARN/);

  const statements = allPolicyStatements(mustResources(template));
  const hasConditionalExactGrant = statements.some((statement) =>
    extractStatementResources(statement).some((resource) => mustJSON(resource).includes("LesserHostInstanceKeyARN")),
  );
  assert.equal(hasConditionalExactGrant, true);
});

test("managed template requires app-scoped Lesser parameter paths", () => {
  const template = synthManagedTemplate();
  const params = mustRecord(template.Parameters, "template missing Parameters");

  for (const name of [
    "JWTSecretArnParamPath",
    "JWTSecretKeyArnParamPath",
    "LesserStageDomainParamPath",
    "LesserTableNameParamPath",
  ]) {
    const param = mustRecord(params[name], `template missing ${name} parameter`);
    assert.equal(Object.hasOwn(param, "Default"), false, `${name} must not have a default`);
  }
});

test("lesser table policy uses least-privilege primary table access", () => {
  const resources = mustResources(synthRuntimeTemplate());
  const lesserTableStatements = policyStatementsForLambda(resources, mcpHandlerLambdaFunction(resources)).filter((statement) =>
    mustJSON(extractStatementResources(statement)).includes("/theory/dev/lesser/exports/v1/table_name"),
  );

  assert.equal(lesserTableStatements.length, 3);
  const statementJSON = mustJSON(lesserTableStatements);
  for (const want of [
    '"dynamodb:DescribeTable"',
    '"dynamodb:GetItem"',
    '"dynamodb:PutItem"',
    '"dynamodb:Query"',
  ]) {
    assert.equal(statementJSON.includes(want), true, `expected ${want} in ${statementJSON}`);
  }
  for (const unwanted of [
    '"dynamodb:BatchGetItem"',
    '"dynamodb:BatchWriteItem"',
    '"dynamodb:ConditionCheckItem"',
    '"dynamodb:DeleteItem"',
    '"dynamodb:Scan"',
    '"dynamodb:UpdateItem"',
    "/index/",
  ]) {
    assert.equal(statementJSON.includes(unwanted), false, `expected policy to omit ${unwanted}: ${statementJSON}`);
  }

  let foundDescribe = false;
  let foundRead = false;
  let foundMemoryWrite = false;
  for (const statement of lesserTableStatements) {
    const json = mustJSON(statement);
    if (json.includes('"dynamodb:DescribeTable"')) {
      foundDescribe = true;
      assert.equal(json.includes('"Condition"'), false, `DescribeTable should not carry LeadingKeys: ${json}`);
      continue;
    }
    if (json.includes('"dynamodb:Query"') || json.includes('"dynamodb:GetItem"')) {
      foundRead = true;
      for (const want of [
        '"dynamodb:Query"',
        '"dynamodb:GetItem"',
        '"dynamodb:LeadingKeys"',
        '"LBMEMORY#*"',
        '"SOUL_BODY_BINDING_USERNAME#*"',
        '"INSTANCE#CONFIG"',
      ]) {
        assert.equal(json.includes(want), true, `expected read policy to contain ${want}: ${json}`);
      }
      assert.equal(json.includes('"dynamodb:PutItem"'), false, `read and write policies must be split: ${json}`);
      continue;
    }
    if (json.includes('"dynamodb:PutItem"')) {
      foundMemoryWrite = true;
      for (const want of [
        '"dynamodb:PutItem"',
        '"dynamodb:LeadingKeys"',
        '"LBMEMORY#*"',
      ]) {
        assert.equal(json.includes(want), true, `expected write policy to contain ${want}: ${json}`);
      }
      for (const unwanted of [
        '"SOUL_BODY_BINDING_USERNAME#*"',
        '"INSTANCE#CONFIG"',
        '"dynamodb:Query"',
        '"dynamodb:GetItem"',
      ]) {
        assert.equal(json.includes(unwanted), false, `expected write policy to omit ${unwanted}: ${json}`);
      }
    }
  }

  assert.equal(foundDescribe && foundRead && foundMemoryWrite, true, mustJSON(lesserTableStatements));
});

test("managed template pins MCP table logical IDs", () => {
  const got = dynamoTableLogicalIDs(mustResources(synthManagedTemplate()));
  for (const want of [
    "McpServerSessionTable469EA0FB",
    "McpServerStreamTableC6A2DC7E",
    "McpServerTaskTable72DDFBBB",
  ]) {
    assert.equal(got.includes(want), true, `expected managed template to preserve ${want}; got ${got.join(", ")}`);
  }
});

test("runtime stack uses AppTheory durable stream table schema", () => {
  const template = synthRuntimeTemplate();
  const resources = mustResources(template);
  const streamTable = findDynamoTableByName(resources, "theory-dev-mcp-streams-v2");
  const props = mustRecord(streamTable.Properties, "stream table missing Properties");
  const keySchema = mustArray(props.KeySchema, "stream table missing KeySchema");

  assert.equal(keySchema.length, 2);
  assert.match(mustJSON(keySchema[0]), /"AttributeName":"sessionId"/);
  assert.match(mustJSON(keySchema[1]), /"AttributeName":"eventId"/);

  const vars = lambdaEnvironmentVariables(mcpHandlerLambdaFunction(resources));
  assert.equal(vars.MCP_STREAM_TTL_MINUTES, "60");
  assert.equal(Object.hasOwn(vars, "MCP_STREAM_SPILL_BUCKET"), true);
  assert.equal(vars.MCP_STREAM_SPILL_PREFIX, "mcp-stream-events");
  assert.equal(vars.MCP_STREAM_SPILL_INLINE_MAX_BYTES, "32768");
  assert.equal(vars.MCP_STREAM_MAX_EVENT_BYTES, "10485760");

  const spillProps = mustRecord(findStreamSpillBucket(resources).Properties, "spill bucket missing Properties");
  assert.match(mustJSON(spillProps.PublicAccessBlockConfiguration), /"BlockPublicAcls":true/);
  assert.match(mustJSON(spillProps.PublicAccessBlockConfiguration), /"RestrictPublicBuckets":true/);
  assert.match(mustJSON(spillProps.BucketEncryption), /"SSEAlgorithm":"AES256"/);
  assert.match(mustJSON(spillProps.LifecycleConfiguration), /"ExpirationInDays":1/);
});

test("runtime stack provisions internal MCP task storage without SSM export", () => {
  const template = synthRuntimeTemplate();
  const resources = mustResources(template);
  const taskTable = findDynamoTableByName(resources, "theory-dev-mcp-tasks");
  const props = mustRecord(taskTable.Properties, "task table missing Properties");
  const keySchema = mustArray(props.KeySchema, "task table missing KeySchema");

  assert.equal(keySchema.length, 2);
  assert.match(mustJSON(keySchema[0]), /"AttributeName":"sessionId"/);
  assert.match(mustJSON(keySchema[1]), /"AttributeName":"taskId"/);
  assert.equal(dynamoTableTtlAttribute("McpServerTaskTable72DDFBBB", props), "expiresAt");

  const vars = lambdaEnvironmentVariables(mcpHandlerLambdaFunction(resources));
  assert.equal(mustJSON(vars.MCP_TASK_TABLE), '{"Ref":"McpServerTaskTable72DDFBBB"}');
  assert.equal(vars.MCP_TASK_TTL_MINUTES, "10");
  assert.equal(hasSSMParameterByName(resources, "/theory/dev/lesser-body/exports/v1/mcp_task_table_name"), false);
});

test("remote MCP server uses actor-path wiring", () => {
  const templateJSON = mustJSON(synthRuntimeTemplate());
  for (const want of [
    "/POST/mcp/*",
    "/GET/mcp/*",
    "/DELETE/mcp/*",
    "/GET/.well-known/oauth-protected-resource/mcp/*",
    "/.well-known/mcp.json",
  ]) {
    assert.equal(templateJSON.includes(want), true, `expected template to contain ${want}`);
  }
  for (const unwanted of [
    '"/POST/mcp"',
    '"/GET/mcp"',
    '"/DELETE/mcp"',
  ]) {
    assert.equal(templateJSON.includes(unwanted), false, `expected actorPath template to omit ${unwanted}`);
  }
});

test("runtime stack preserves named MCP table fingerprints", () => {
  const got = dynamoTableFingerprints(mustResources(synthRuntimeTemplate()));
  const want: Record<string, DynamoTableFingerprint> = {
    "theory-dev-mcp-sessions": {
      logical_id: "McpServerSessionTable469EA0FB",
      table_name: "theory-dev-mcp-sessions",
      partition_key: "sessionId",
      sort_key: "",
      ttl_attribute: "expiresAt",
    },
    "theory-dev-mcp-streams-v2": {
      logical_id: "McpServerStreamTableC6A2DC7E",
      table_name: "theory-dev-mcp-streams-v2",
      partition_key: "sessionId",
      sort_key: "eventId",
      ttl_attribute: "expiresAt",
    },
    "theory-dev-mcp-tasks": {
      logical_id: "McpServerTaskTable72DDFBBB",
      table_name: "theory-dev-mcp-tasks",
      partition_key: "sessionId",
      sort_key: "taskId",
      ttl_attribute: "expiresAt",
    },
  };

  for (const [tableName, fingerprint] of Object.entries(want)) {
    assert.deepEqual(got[tableName], fingerprint);
  }
});

test("stream table export name stays stable across baseline reset", () => {
  const resources = mustResources(synthRuntimeTemplate());
  const streamExport = findSSMParameterByName(resources, "/theory/dev/lesser-body/exports/v1/mcp_stream_table_name");
  const props = mustRecord(streamExport.Properties, "stream export missing Properties");

  assert.equal(mustJSON(props.Value), '{"Ref":"McpServerStreamTableC6A2DC7E"}');
});

test("runtime stack provisions instance-plane lambda with owned tables", () => {
  const resources = mustResources(synthRuntimeTemplate());
  const instanceHandler = instanceMcpHandlerLambdaFunction(resources);
  const instanceHandlerProps = mustRecord(instanceHandler.Properties, "instance lambda missing Properties");
  const vars = lambdaEnvironmentVariables(instanceHandler);

  assert.equal(instanceHandlerProps.Handler, "instance");
  assert.equal(vars.INSTANCE_MCP_ENDPOINT, "https://api.dev.example.com/instance/{surface}/mcp");
  assert.equal(mustJSON(vars.INSTANCE_CONTENT_TABLE), '{"Ref":"InstanceContentTable"}');
  assert.equal(mustJSON(vars.INSTANCE_REGISTRY_TABLE), '{"Ref":"InstanceRegistryTable"}');
  assert.equal(mustJSON(vars.INSTANCE_GRANT_TABLE), '{"Ref":"InstanceGrantTable"}');
  assert.equal(mustJSON(vars.INSTANCE_SESSION_TABLE), '{"Ref":"InstanceSessionTable"}');
  assert.equal(mustJSON(vars.JWT_SECRET_ARN), '"{{resolve:ssm:/theory/shared/secrets/jwt-secret-arn}}"');
  assert.equal(mustJSON(vars.LESSER_TABLE_NAME), '"{{resolve:ssm:/theory/dev/lesser/exports/v1/table_name}}"');
  assert.equal(vars.LESSER_API_BASE_URL, "https://api.dev.example.com");

  const got = dynamoTableFingerprints(resources);
  const want: Record<string, DynamoTableFingerprint> = {
    "theory-dev-instance-content": {
      logical_id: "InstanceContentTable",
      table_name: "theory-dev-instance-content",
      partition_key: "pk",
      sort_key: "sk",
      ttl_attribute: "",
    },
    "theory-dev-instance-registry": {
      logical_id: "InstanceRegistryTable",
      table_name: "theory-dev-instance-registry",
      partition_key: "pk",
      sort_key: "sk",
      ttl_attribute: "",
    },
    "theory-dev-instance-grants": {
      logical_id: "InstanceGrantTable",
      table_name: "theory-dev-instance-grants",
      partition_key: "pk",
      sort_key: "sk",
      ttl_attribute: "",
    },
    "theory-dev-instance-sessions": {
      logical_id: "InstanceSessionTable",
      table_name: "theory-dev-instance-sessions",
      partition_key: "pk",
      sort_key: "sk",
      ttl_attribute: "expiresAt",
    },
  };
  for (const [tableName, fingerprint] of Object.entries(want)) {
    assert.deepEqual(got[tableName], fingerprint);
  }

  const statements = allPolicyStatements(resources).filter((statement) =>
    mustJSON(extractStatementResources(statement)).includes("InstanceContentTable"),
  );
  const instanceTablePolicyJSON = mustJSON(statements);
  for (const wantAction of [
    '"dynamodb:BatchGetItem"',
    '"dynamodb:BatchWriteItem"',
    '"dynamodb:DeleteItem"',
    '"dynamodb:DescribeTable"',
    '"dynamodb:GetItem"',
    '"dynamodb:PutItem"',
    '"dynamodb:Query"',
    '"dynamodb:UpdateItem"',
  ]) {
    assert.equal(instanceTablePolicyJSON.includes(wantAction), true, `expected ${wantAction} in ${instanceTablePolicyJSON}`);
  }
  assert.equal(instanceTablePolicyJSON.includes('"dynamodb:Scan"'), false, `expected instance table policy to omit Scan: ${instanceTablePolicyJSON}`);
});

test("instance-plane lambda receives managed Lesser/Host genesis configuration", () => {
  const resources = mustResources(synthManagedTemplate());
  const instanceHandler = instanceMcpHandlerLambdaFunction(resources);
  const vars = lambdaEnvironmentVariables(instanceHandler);

  assert.match(mustJSON(vars.LESSER_TABLE_NAME), /LesserTableNameParamPath|lesser\/exports\/v1\/table_name/);
  assert.match(mustJSON(vars.LESSER_API_BASE_URL), /https:\/\/api\./);
  assert.match(mustJSON(vars.LESSER_HOST_INSTANCE_KEY_ARN), /LesserHostInstanceKeyARN/);

  const statements = policyStatementsForLambda(resources, instanceHandler);
  const statementJSON = mustJSON(statements);
  for (const want of [
    '"dynamodb:DescribeTable"',
    '"dynamodb:GetItem"',
    '"dynamodb:Query"',
    '"dynamodb:LeadingKeys"',
    '"INSTANCE#CONFIG"',
    '"secretsmanager:GetSecretValue"',
    '"secretsmanager:DescribeSecret"',
    "instance-key*",
    "lesser-host/lab/instances",
    "LesserHostInstanceKeyARN",
  ]) {
    assert.equal(statementJSON.includes(want), true, `expected instance policy to contain ${want}: ${statementJSON}`);
  }
  for (const unwanted of ['"LBMEMORY#*"', '"SOUL_BODY_BINDING_USERNAME#*"']) {
    assert.equal(statementJSON.includes(unwanted), false, `instance trust-config policy must omit ${unwanted}: ${statementJSON}`);
  }
});

test("runtime stack publishes additive instance-plane SSM exports", () => {
  const resources = mustResources(synthRuntimeTemplate());
  const expected: Record<string, string> = {
    "/theory/dev/lesser-body/exports/v1/instance_mcp_lambda_arn": "InstanceMcpHandler",
    "/theory/dev/lesser-body/exports/v1/instance_content_table_name": "InstanceContentTable",
    "/theory/dev/lesser-body/exports/v1/instance_registry_table_name": "InstanceRegistryTable",
    "/theory/dev/lesser-body/exports/v1/instance_grant_table_name": "InstanceGrantTable",
    "/theory/dev/lesser-body/exports/v1/instance_session_table_name": "InstanceSessionTable",
  };

  for (const [name, ref] of Object.entries(expected)) {
    const resource = findSSMParameterByName(resources, name);
    const props = mustRecord(resource.Properties, `export ${name} missing Properties`);
    assert.equal(mustJSON(props.Value).includes(ref), true, `expected ${name} to reference ${ref}, got ${mustJSON(props.Value)}`);
  }

  const endpoint = findSSMParameterByName(resources, "/theory/dev/lesser-body/exports/v1/instance_mcp_endpoint_url");
  const endpointProps = mustRecord(endpoint.Properties, "instance endpoint export missing Properties");
  assert.equal(endpointProps.Value, "https://api.dev.example.com/instance/{surface}/mcp");
});

interface CloudFormationTemplate {
  readonly [key: string]: unknown;
  readonly Resources?: unknown;
  readonly Parameters?: unknown;
}

interface CloudFormationResource {
  readonly [key: string]: unknown;
  readonly Type?: unknown;
  readonly Properties?: unknown;
}

type CloudFormationRecord = Record<string, unknown>;

interface DynamoTableFingerprint {
  readonly logical_id: string;
  readonly table_name: string;
  readonly partition_key: string;
  readonly sort_key: string;
  readonly ttl_attribute: string;
}

function synthRuntimeTemplate(): CloudFormationTemplate {
  const app = new cdk.App({
    outdir: mkdtempSync(join(tmpdir(), "lesser-body-cdk-test-")),
    analyticsReporting: false,
    treeMetadata: false,
  });
  const stack = new cdk.Stack(app, "TestStack", {
    env: { account: "123456789012", region: "us-east-1" },
  });

  configureLesserBodyStack(stack, {
    appName: "theory",
    stage: "dev",
    code: lambda.Code.fromAsset(fakeLambdaAssetDir()),
    instanceCode: lambda.Code.fromAsset(fakeLambdaAssetDir()),
    serviceVersion: "test",
    publicEndpoint: "https://api.dev.example.com/mcp/{actor}",
    instancePublicEndpoint: "https://api.dev.example.com/instance/{surface}/mcp",
    lesserApiBaseUrl: "https://api.dev.example.com",
    allowedOrigins: "https://claude.ai",
    jwtSecretArnParamPath: "/theory/shared/secrets/jwt-secret-arn",
    jwtSecretKeyParamPath: "/theory/shared/kms/encryption-key-arn",
    lesserTableParamPath: "/theory/dev/lesser/exports/v1/table_name",
  });

  return stackTemplate(app, stack);
}

function synthManagedTemplate(): CloudFormationTemplate {
  const app = new cdk.App({
    outdir: mkdtempSync(join(tmpdir(), "lesser-body-cdk-test-")),
    analyticsReporting: false,
    treeMetadata: false,
  });
  const stack = new LesserBodyDeployTemplateStack(app, "TestStack", {
    env: { account: "123456789012", region: "us-east-1" },
    serviceVersion: "test",
    stage: "dev",
  });

  return stackTemplate(app, stack);
}

function stackTemplate(app: cdk.App, stack: cdk.Stack): CloudFormationTemplate {
  const assembly = app.synth();
  return assembly.getStackArtifact(stack.artifactId).template as CloudFormationTemplate;
}

function fakeLambdaAssetDir(): string {
  const dir = mkdtempSync(join(tmpdir(), "lesser-body-lambda-asset-"));
  const bootstrap = join(dir, "bootstrap");
  writeFileSync(bootstrap, "#!/bin/sh\nexit 0\n");
  chmodSync(bootstrap, 0o755);
  return dir;
}

function mustResources(template: CloudFormationTemplate): Record<string, CloudFormationResource> {
  return mustRecord(template.Resources, "template missing Resources") as Record<string, CloudFormationResource>;
}

function allPolicyStatements(resources: Record<string, CloudFormationResource>): CloudFormationRecord[] {
  const out: CloudFormationRecord[] = [];
  for (const resource of Object.values(resources)) {
    if (resource.Type !== "AWS::IAM::Policy") {
      continue;
    }
    out.push(...extractPolicyStatements(resource));
  }
  assert.notEqual(out.length, 0, "expected at least one IAM policy statement");
  return out;
}

function policyStatementsForLambda(
  resources: Record<string, CloudFormationResource>,
  lambdaResource: CloudFormationResource,
): CloudFormationRecord[] {
  const lambdaProps = mustRecord(lambdaResource.Properties, "lambda missing Properties");
  const role = mustRecord(lambdaProps.Role, "lambda missing execution role");
  const roleGetAtt = mustArray(role["Fn::GetAtt"], "lambda role must use Fn::GetAtt");
  const roleLogicalId = String(roleGetAtt[0] ?? "");
  if (!roleLogicalId) {
    throw new Error("lambda role missing logical id");
  }

  const out: CloudFormationRecord[] = [];
  for (const resource of Object.values(resources)) {
    if (resource.Type !== "AWS::IAM::Policy") {
      continue;
    }
    const props = mustRecord(resource.Properties, "policy missing Properties");
    if (!mustJSON(props.Roles).includes(`"Ref":"${roleLogicalId}"`)) {
      continue;
    }
    out.push(...extractPolicyStatements(resource));
  }
  assert.notEqual(out.length, 0, `expected IAM policy statements for ${roleLogicalId}`);
  return out;
}

function extractPolicyStatements(policy: CloudFormationResource): CloudFormationRecord[] {
  const props = mustRecord(policy.Properties, "policy missing Properties");
  const doc = mustRecord(props.PolicyDocument, "policy missing PolicyDocument");
  return mustArray(doc.Statement, "policy document missing Statement list").map((statement) =>
    mustRecord(statement, "policy Statement entry has unexpected type"),
  );
}

function statementResourcesContain(statements: CloudFormationRecord[], want: string): boolean {
  return statements.some((statement) =>
    extractStatementResources(statement).some((resource) => mustJSON(resource).includes(want)),
  );
}

function extractStatementResources(statement: CloudFormationRecord): unknown[] {
  if (Array.isArray(statement.Resource)) {
    return statement.Resource;
  }
  if (Object.hasOwn(statement, "Resource")) {
    return [statement.Resource];
  }
  return [];
}

function mcpHandlerLambdaFunction(resources: Record<string, CloudFormationResource>): CloudFormationResource {
  for (const resource of Object.values(resources)) {
    if (resource.Type !== "AWS::Lambda::Function") {
      continue;
    }
    const props = mustRecord(resource.Properties, "lambda missing Properties");
    if (props.Handler === "bootstrap" && Object.hasOwn(lambdaEnvironmentVariables(resource), "MCP_ENDPOINT")) {
      return resource;
    }
  }
  throw new Error("template missing MCP handler AWS::Lambda::Function resource");
}

function instanceMcpHandlerLambdaFunction(resources: Record<string, CloudFormationResource>): CloudFormationResource {
  for (const resource of Object.values(resources)) {
    if (resource.Type !== "AWS::Lambda::Function") {
      continue;
    }
    const props = mustRecord(resource.Properties, "lambda missing Properties");
    if (props.Handler === "instance" && Object.hasOwn(lambdaEnvironmentVariables(resource), "INSTANCE_MCP_ENDPOINT")) {
      return resource;
    }
  }
  throw new Error("template missing instance MCP handler AWS::Lambda::Function resource");
}

function lambdaEnvironmentVariables(lambdaResource: CloudFormationResource): CloudFormationRecord {
  const props = mustRecord(lambdaResource.Properties, "lambda missing Properties");
  const env = mustRecord(props.Environment, "lambda missing Environment");
  return mustRecord(env.Variables, "lambda missing Environment.Variables");
}

function findDynamoTableByName(resources: Record<string, CloudFormationResource>, tableName: string): CloudFormationResource {
  for (const resource of Object.values(resources)) {
    if (resource.Type !== "AWS::DynamoDB::Table") {
      continue;
    }
    const props = mustRecord(resource.Properties, "table missing Properties");
    if (props.TableName === tableName) {
      return resource;
    }
  }
  throw new Error(`template missing AWS::DynamoDB::Table ${JSON.stringify(tableName)}`);
}

function findSSMParameterByName(resources: Record<string, CloudFormationResource>, paramName: string): CloudFormationResource {
  const resource = ssmParameterByName(resources, paramName);
  if (!resource) {
    throw new Error(`template missing AWS::SSM::Parameter ${JSON.stringify(paramName)}`);
  }
  return resource;
}

function hasSSMParameterByName(resources: Record<string, CloudFormationResource>, paramName: string): boolean {
  return ssmParameterByName(resources, paramName) !== undefined;
}

function ssmParameterByName(resources: Record<string, CloudFormationResource>, paramName: string): CloudFormationResource | undefined {
  for (const resource of Object.values(resources)) {
    if (resource.Type !== "AWS::SSM::Parameter") {
      continue;
    }
    const props = mustRecord(resource.Properties, "SSM parameter missing Properties");
    if (props.Name === paramName) {
      return resource;
    }
  }
  return undefined;
}

function findStreamSpillBucket(resources: Record<string, CloudFormationResource>): CloudFormationResource {
  for (const resource of Object.values(resources)) {
    if (resource.Type !== "AWS::S3::Bucket") {
      continue;
    }
    const props = mustRecord(resource.Properties, "bucket missing Properties");
    if (Object.hasOwn(props, "LifecycleConfiguration")) {
      return resource;
    }
  }
  throw new Error("template missing stream spill AWS::S3::Bucket");
}

function dynamoTableLogicalIDs(resources: Record<string, CloudFormationResource>): string[] {
  return Object.entries(resources)
    .filter(([, resource]) => resource.Type === "AWS::DynamoDB::Table")
    .map(([logicalId]) => logicalId)
    .sort();
}

function dynamoTableFingerprints(resources: Record<string, CloudFormationResource>): Record<string, DynamoTableFingerprint> {
  const out: Record<string, DynamoTableFingerprint> = {};
  for (const logicalId of Object.keys(resources).sort()) {
    const resource = resources[logicalId];
    if (resource.Type !== "AWS::DynamoDB::Table") {
      continue;
    }
    const props = mustRecord(resource.Properties, `table ${logicalId} missing Properties`);
    const tableName = props.TableName;
    if (typeof tableName !== "string" || tableName === "") {
      throw new Error(`table ${logicalId} missing TableName`);
    }
    const [partitionKey, sortKey] = dynamoTableKeyNames(logicalId, props);
    out[tableName] = {
      logical_id: logicalId,
      table_name: tableName,
      partition_key: partitionKey,
      sort_key: sortKey,
      ttl_attribute: dynamoTableTtlAttribute(logicalId, props),
    };
  }
  return out;
}

function dynamoTableKeyNames(logicalId: string, props: CloudFormationRecord): [string, string] {
  const keySchema = mustArray(props.KeySchema, `table ${logicalId} missing KeySchema`);
  let partitionKey = "";
  let sortKey = "";
  for (const raw of keySchema) {
    const entry = mustRecord(raw, `table ${logicalId} KeySchema entry has unexpected type`);
    if (entry.KeyType === "HASH") {
      partitionKey = String(entry.AttributeName ?? "");
    } else if (entry.KeyType === "RANGE") {
      sortKey = String(entry.AttributeName ?? "");
    }
  }
  if (!partitionKey) {
    throw new Error(`table ${logicalId} missing HASH key`);
  }
  return [partitionKey, sortKey];
}

function dynamoTableTtlAttribute(logicalId: string, props: CloudFormationRecord): string {
  if (props.TimeToLiveSpecification === undefined) {
    return "";
  }
  const spec = mustRecord(props.TimeToLiveSpecification, `table ${logicalId} TimeToLiveSpecification has unexpected type`);
  if (typeof spec.AttributeName !== "string" || spec.AttributeName === "") {
    throw new Error(`table ${logicalId} missing TTL attribute name`);
  }
  return spec.AttributeName;
}

function mustRecord(value: unknown, message: string): CloudFormationRecord {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new Error(message);
  }
  return value as CloudFormationRecord;
}

function mustArray(value: unknown, message: string): unknown[] {
  if (!Array.isArray(value)) {
    throw new Error(message);
  }
  return value;
}

function mustJSON(value: unknown): string {
  return JSON.stringify(value);
}
