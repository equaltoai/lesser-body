#!/usr/bin/env node
import * as cdk from "aws-cdk-lib";
import { LesserBodyDeployTemplateStack } from "../lib/lesser-body-stack";

const args = parseArgs(process.argv.slice(2));
const outdir = args.outdir;
const version = args.version;
const stage = args.stage;

if (!outdir) {
  throw new Error("--outdir is required");
}
if (!version) {
  throw new Error("--version is required");
}
if (!stage) {
  throw new Error("--stage is required");
}

const app = new cdk.App({
  outdir,
  analyticsReporting: false,
  autoSynth: false,
  treeMetadata: false,
});

new LesserBodyDeployTemplateStack(app, "LesserBodyManagedTemplate", {
  serviceVersion: version,
  stage,
});

app.synth();

function parseArgs(argv: string[]): Record<string, string> {
  const out: Record<string, string> = {};
  for (let i = 0; i < argv.length; i += 1) {
    const arg = argv[i];
    if (!arg.startsWith("--")) {
      throw new Error(`unexpected argument: ${arg}`);
    }
    const eq = arg.indexOf("=");
    if (eq >= 0) {
      out[arg.slice(2, eq)] = arg.slice(eq + 1);
      continue;
    }
    const key = arg.slice(2);
    const value = argv[i + 1];
    if (value === undefined || value.startsWith("--")) {
      throw new Error(`missing value for ${arg}`);
    }
    out[key] = value;
    i += 1;
  }
  return out;
}
