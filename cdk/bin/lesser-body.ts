#!/usr/bin/env node
import * as cdk from "aws-cdk-lib";
import { LesserBodyStack } from "../lib/lesser-body-stack";

const app = new cdk.App();

const appName = contextString(app, "app") || "lesser";
const stage = (contextString(app, "stage") || "dev").trim().toLowerCase();
if (!["dev", "staging", "live"].includes(stage)) {
  throw new Error(`invalid stage ${JSON.stringify(stage)} (expected dev, staging, live)`);
}

const baseDomain = contextString(app, "baseDomain").trim().toLowerCase().replace(/\.+$/, "");
const lesserHostInstanceKeyArn = contextString(app, "lesserHostInstanceKeyArn") || process.env.LESSER_HOST_INSTANCE_KEY_ARN || "";

const awsAccount = process.env.CDK_DEFAULT_ACCOUNT?.trim();
const awsRegion = process.env.CDK_DEFAULT_REGION?.trim() || process.env.AWS_REGION?.trim();
if (!awsAccount) {
  throw new Error("CDK_DEFAULT_ACCOUNT is not set (run via the CDK CLI)");
}
if (!awsRegion) {
  throw new Error("CDK_DEFAULT_REGION is not set (run via the CDK CLI)");
}

new LesserBodyStack(app, `${appName}-${stage}-lesser-body`, {
  env: { account: awsAccount, region: awsRegion },
  appName,
  stage,
  baseDomain,
  lesserHostInstanceKeyArn,
});

app.synth();

function contextString(app: cdk.App, key: string): string {
  const value = app.node.tryGetContext(key);
  if (value === undefined || value === null) {
    return "";
  }
  const out = String(value).trim();
  if (!out || out.toLowerCase() === "<nil>") {
    return "";
  }
  return out;
}
