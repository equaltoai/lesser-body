# lesser-body: AppTheory App Bootstrap Plan

Generated: 2026-02-21

Scope summary: agent-code mcp system to support lesser as a plugin

This document is a plan for bootstrapping a new AppTheory application. It is **instructions only**: no application code is written by this action.

The machine-readable deployment contract consumed by `theory app up/down` is stored in `app-theory/app.json`.

## Outputs

This action writes exactly:
- `app-theory/init.md` (this file)
- `app-theory/app.json` (deployment contract)

## Destination (pinned): AppTheory + TableTheory

This section defines the **pinned destination frameworks**. These values are **constants** provided by the GovTheory pack.

### AppTheory (pinned)
- Go module: `github.com/theory-cloud/apptheory@v0.11.0`
- Go runtime import: `github.com/theory-cloud/apptheory/runtime`
- Docs entrypoints (for tag `v0.11.0`):
  - `docs/getting-started.md`
  - `docs/agentcore-mcp.md`
  - `docs/mcp.md`
  - `docs/migration/from-lift.md`
- Copy/paste dependency command:
  - `go get github.com/theory-cloud/apptheory@v0.11.0`
- Recommended pinned docs links:
  - `https://github.com/theory-cloud/AppTheory/blob/v0.11.0/docs/getting-started.md`
  - `https://github.com/theory-cloud/AppTheory/blob/v0.11.0/docs/agentcore-mcp.md`
  - `https://github.com/theory-cloud/AppTheory/blob/v0.11.0/docs/mcp.md`
  - `https://github.com/theory-cloud/AppTheory/blob/v0.11.0/docs/migration/from-lift.md`
- Recommended pinned CDK docs links:
  - `https://github.com/theory-cloud/AppTheory/blob/v0.11.0/cdk/docs/getting-started.md`
  - `https://github.com/theory-cloud/AppTheory/blob/v0.11.0/cdk/docs/api-reference.md`

### TableTheory (pinned)
- Go module: `github.com/theory-cloud/tabletheory@v1.4.0`
- Docs entrypoints (for tag `v1.4.0`):
  - `docs/getting-started.md`
  - `docs/api-reference.md`
  - `docs/migration-guide.md`
- Copy/paste dependency command:
  - `go get github.com/theory-cloud/tabletheory@v1.4.0`
- Recommended pinned docs links:
  - `https://github.com/theory-cloud/TableTheory/blob/v1.4.0/docs/getting-started.md`
  - `https://github.com/theory-cloud/TableTheory/blob/v1.4.0/docs/api-reference.md`
  - `https://github.com/theory-cloud/TableTheory/blob/v1.4.0/docs/migration-guide.md`

## Local agent execution plan

The goal is to produce a repository that:
- contains your application code (outside `app-theory/`)
- contains a CDK project directory that matches `app-theory/app.json`
- can be deployed/destroyed deterministically across stages using `theory app up` / `theory app down`

### Step 1 — Scaffold the application codebase (outside `app-theory/`)

1) Choose a repo layout for application code (example):
   - `cmd/lesser-body/` (main)
   - `internal/` (implementation)
   - `pkg/` (optional public packages)
2) Initialize the Go module (if not already) and add pinned framework dependencies:
   - Add AppTheory at `v0.11.0`.
   - Add TableTheory at `v1.4.0` if the app uses DynamoDB tables.
3) Follow AppTheory runtime/bootstrap docs and wire your entrypoints.

**Acceptance criteria**
- The repo builds locally with `go build ./...`.
- The repo can run its unit tests (if present) with `go test ./...`.
- `go.mod` reflects the pinned module versions shown in the Destination section.

### Step 2 — Create the CDK project directory and entrypoints

1) Create (or choose) the repo-relative CDK directory specified in `app-theory/app.json`:
   - Contract default: `cdk/`
2) Initialize the CDK project in that directory.
   - Keep dependency installation deterministic:
     - prefer `npm ci` (requires a committed lockfile, e.g. `package-lock.json`)
3) Implement deploy/destroy entrypoints that match the deployment contract commands:
   - Deploy must be stage-aware via CDK context: `-c stage=<stage>`
   - Destroy must target the same stage context
   - Ensure the stage context influences stack names and/or environment selection so you do not deploy to the wrong account/region

**Acceptance criteria**
- From within the CDK directory, the contract’s “up” command deploys successfully for a chosen stage.
- From within the CDK directory, the contract’s “down” command destroys successfully for the same stage.
- The CDK project has a lockfile committed so `npm ci` is stable across machines/CI.

### Step 3 — Keep the contract and the repo in sync

If you change any of the following:
- CDK directory location
- deploy/destroy commands
- how the stage parameter is passed to CDK

…then update `app-theory/app.json` accordingly so `theory app up/down` remain correct.

## Deployment contract

`theory app up` and `theory app down` read the file:
- `app-theory/app.json`

That contract defines:
- `schema`: contract version
- `frameworks`: pinned destination details (AppTheory + TableTheory)
- `cdk.dir`: repo-relative CDK directory
- `cdk.up`: deploy command (expects AWS profile + stage at runtime)
- `cdk.down`: destroy command (expects AWS profile + stage at runtime)

### Using the contract with `theory app up/down`

- Stage behavior:
  - default: `lab`
  - override: `--stage live`
- AWS profile behavior:
  - pass explicitly: `--aws-profile <name>`
  - or via environment: `AWS_PROFILE=<name>`

Copy/paste examples:

```bash
# Default stage (lab) using an AWS profile via environment
AWS_PROFILE=my-profile theory app up

# Explicitly set stage and AWS profile via flags
theory app up --aws-profile my-profile --stage lab

# Destroy the live stage
theory app down --aws-profile my-profile --stage live
```

Notes:
- The `theory` CLI substitutes the runtime values into the contract placeholders used by `cdk.up` / `cdk.down`.
- Keep the CDK commands deterministic (use `npm ci`) and stage-aware (use `-c stage=...`).
