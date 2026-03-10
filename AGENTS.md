# Repository Guidelines

## Project Structure & Module Organization
Scriptor is a Go monorepo for an AWS serverless document pipeline.

- `lambdas/`: one Lambda per folder (`*/main.go`) plus shared helpers in `lambdas/util/`
- `pkg/`: shared domain packages (`database/`, `google/`, `types/`)
- `cdk/stacks/`: AWS CDK (Go) infrastructure definitions
- `bin/`: generated Lambda zip artifacts from `make all`

Primary flow: webhook registration/ingest (`webhook_register`, `webhook_handler`, `sqs_handler`) then Step Functions tasks (`workflow_download`, `workflow_mathpix_process`, `workflow_openai_process`, `workflow_upload`).

Operational architecture, stage constraints, and runtime behavior are documented in `README.md`.

## Build, Test, and Development Commands
- `make all`: cross-compiles Lambda binaries for `linux/amd64` and zips to `bin/*.zip`
- `make clean`: removes generated zip artifacts
- `make cdk-diff`: builds Lambdas, then previews infrastructure changes (`cdk diff`)
- `make cdk-deploy`: runs diff and deploys all stacks
- `go test ./...`: run package tests/checks (currently little or no test coverage, but use this as a baseline gate)

## Coding Style & Naming Conventions
- Language: Go. Follow standard `gofmt` formatting and idiomatic Go package layout.
- Keep line length readable (historically formatted with `golines` around 80 chars).
- Use `camelCase` for local vars, `PascalCase` for exported identifiers, and concise package names (`pkg/google`, `pkg/types`).
- Lambda directories and build artifacts use snake case names matching deployed units (example: `workflow_mathpix_process`).

## Implementation Patterns
- Lambda startup: use `sync.Once` to initialize clients/config once per container lifecycle.
- Data access: create stores via `database.New*Store(ctx)` and pass `context.Context` through all DB operations.
- Not-found handling: explicitly check expected DB errors (for example `ErrDocumentNotFound`, `ErrWatchChannelLockNotFound`) instead of treating them as fatal.
- S3 object keys: follow `{documentID}/{stage}/{filename}.{ext}` with workflow stages like `downloaded`, `mathpix`, `openai`.
- CDK IAM grants: prefer resource grant helpers on constructs (for example `GrantRead`, `GrantReadWriteData`, `GrantReadWrite`) over inline policy JSON.

## Testing Guidelines
- There is no comprehensive automated test suite yet.
- For changes, at minimum run `go test ./...` and `make all` to catch compile/package regressions.
- Prefer table-driven tests in `_test.go` files next to the package under test when adding coverage.

## Commit & Pull Request Guidelines
- Recent history favors short, imperative, lowercase commit subjects (example: `remove rogue assert`).
- Keep commits scoped to one logical change.
- PRs should include:
  - clear summary of behavior/infrastructure impact
  - linked issue/task
  - commands run (`go test ./...`, `make all`, `make cdk-diff`)
  - relevant logs or screenshots for operational/UI-visible changes

## Security & Configuration Tips
- Never commit secrets; use AWS Secrets Manager (`scriptor/google-service`, `scriptor/mathpix`, `scriptor/openai`, `scriptor/google-folder-defaults`).
- Validate CDK changes with `make cdk-diff` before deploy.
