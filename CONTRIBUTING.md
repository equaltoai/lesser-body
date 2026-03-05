# Contributing to lesser-body

<!-- AI Training: Contributor workflow and repo conventions for lesser-body -->

This doc describes how to contribute changes safely to the `lesser-body` repository.

## Quick start (local dev)

Run unit tests:

```bash
go test ./...
```

Build the Lambda artifact:

```bash
bash scripts/build.sh
```

Sanity check CDK synth:

```bash
export CDK_DEFAULT_ACCOUNT="000000000000"
export CDK_DEFAULT_REGION="us-east-1"

cd cdk
npm ci
npx cdk synth -c app=lesser -c stage=dev -c baseDomain=example.com
```

## Before opening a PR

✅ CORRECT: keep changes small, testable, and tied to the repo’s deployment contract.

Recommended checks:

```bash
gofmt -w .
go test ./...
```

If you changed infrastructure:

```bash
cd cdk
npm ci
npx cdk synth -c app=lesser -c stage=dev -c baseDomain=example.com
```

## Repo conventions

### Docs are canonical

✅ CORRECT: document operator/dev workflows in `docs/`.

Spec/plan docs (`SPEC.md`, `ROADMAP.md`) are useful references, but should not be the only place a workflow is described.

### SSM-first wiring (no CFN exports/imports)

Cross-stack wiring between Lesser and lesser-body should use **SSM parameters** with stable names that include:

- app slug
- stage
- project slug
- version segment (for example: `exports/v1`)

See `docs/configuration.md`.

### Keep auth predictable

When changing auth or tool authorization:

- preserve the `read|write|admin` scope model unless you are intentionally migrating it
- avoid logging secrets or bearer tokens
- add/update unit tests (see `internal/mcpapp/app_test.go`)

