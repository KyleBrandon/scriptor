# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Scriptor is a serverless document processing pipeline on AWS. It monitors a Google Drive folder for PDFs, converts them to Markdown via Mathpix, cleans up the Markdown with ChatGPT, and uploads the results back to Google Drive. Built with Go and AWS CDK (Go bindings).

## Build & Deploy Commands

```bash
make all              # Build all Lambda functions (zips in ./bin)
make clean            # Remove generated zip files
make cdk-diff         # Build lambdas + show CDK diff
make cdk-deploy       # Build, diff, deploy all stacks to AWS
```

Lambdas are cross-compiled for `GOOS=linux GOARCH=amd64` and packaged as `bootstrap` executables for the `PROVIDED_AL2` runtime.

There are no automated tests in this codebase.

## Architecture

### Processing Pipeline

1. **webhook_register** — Daily EventBridge trigger registers/renews Google Drive watch channels (48h expiry, renewed if expiring within 20h)
2. **webhook_handler** — API Gateway receives Drive notifications, validates against registered watch channels, enqueues to SQS
3. **sqs_handler** — Consumes SQS messages, queries Drive for new files, starts Step Functions execution
4. **Step Functions workflow** (sequential):
   - `workflow_download` → Downloads PDF from Drive to S3
   - `workflow_mathpix_process` → PDF→Markdown via Mathpix API
   - `workflow_chatgpt_process` → Markdown cleanup via OpenAI API
   - `workflow_upload` → Uploads Markdown+PDF to destination folder, archives original

### Code Layout

- `lambdas/` — Each Lambda in its own subdirectory with `main.go`
- `lambdas/util/` — Shared Lambda utilities
- `cdk/stacks/` — CDK infrastructure definitions; `aws_resources.go` defines shared resources (DynamoDB, S3, secrets, SQS), `props.go` has CDK configuration and stack properties, other files define individual Lambda stacks
- `pkg/database/` — DynamoDB abstraction with store interfaces (`DocumentStore`, `WatchChannelStore`)
- `pkg/google/drive.go` — Google Drive client (auth via service account from Secrets Manager)
- `pkg/types/types.go` — All domain types, constants, secret names, S3 bucket names, stage/status enums

### Key Patterns

**Lambda initialization**: All lambdas use `sync.Once` to load config (AWS clients, secrets, DB connections) once per container lifecycle:

```go
var (
    initOnce sync.Once
    cfg      *handlerConfig
)

func initLambda(ctx context.Context) error {
    var err error
    initOnce.Do(func() {
        cfg, err = loadConfiguration(ctx)
    })
    return err
}
```

**Database**: Use `database.New*Store(ctx)` constructors. Check for `ErrDocumentNotFound` and `ErrWatchChannelLockNotFound`. All structs use `dynamodbav` tags. Use `context.Context` for all database calls.

**S3 keys**: `{documentID}/{stage}/{filename}.{ext}` where stage is `downloaded`, `mathpix`, or `chatgpt`.

**CDK permissions**: Use grant methods on CDK constructs:

```go
cfg.GoogleServiceKeySecret.GrantRead(lambda, nil)
cfg.documentTable.GrantReadWriteData(lambda)
cfg.documentBucket.GrantReadWrite(lambda, nil)
```

**Document stages**: `new` → `downloaded` → `mathpix` → `chatgpt` → `uploaded`, each with status tracking (pending/in-progress/complete/error).

### DynamoDB Tables

- `Documents` — Document metadata from Google Drive
- `DocumentProcessingStage` — Tracks documents through workflow stages
- `WatchChannels` — Google Drive watch channel registrations
- `WatchChannelLocks` — Distributed locking for change token management

## AWS Configuration

### Required Secrets (AWS Secrets Manager)

All secrets are "Other type of secret" with key/value pairs:

1. `scriptor/google-service` — Google service account JSON (paste entire JSON in Plaintext section)
2. `scriptor/google-folder-defaults`:
   - `folder_id` — Google Drive folder to monitor
   - `archive_folder_id` — Where to move processed PDFs
   - `destination_folder_id` — Where to upload final Markdown and PDF
3. `scriptor/mathpix`:
   - `mathpix_app_id`
   - `mathpix_app_key`
4. `scriptor/chatgpt`:
   - `api_key`

### Google Drive Setup

- Create a Google Cloud service account with Google Drive API enabled
- Share the Scriptor folder hierarchy with the service account email (grant Editor permissions)
- Service account needs ability to watch, read, create, move files

## Important Constraints

- Workflow timeout: 15 minutes overall, 3 minutes per task
- All timestamps in UTC
- Files with the same name in the same Drive folder are de-duplicated
- Watch channel lock expires after configured duration to handle Lambda failures

## Session Completion

When ending a work session, you MUST complete ALL steps below. Work is NOT complete until `git push` succeeds.

**MANDATORY WORKFLOW:**

1. **File issues for remaining work** — Create issues for anything that needs follow-up
2. **Run quality gates** (if code changed) — Tests, linters, builds
3. **Update issue status** — Close finished work, update in-progress items
4. **PUSH TO REMOTE** — This is MANDATORY:
   ```bash
   git pull --rebase
   bd sync
   git push
   git status  # MUST show "up to date with origin"
   ```
5. **Clean up** — Clear stashes, prune remote branches
6. **Verify** — All changes committed AND pushed
7. **Hand off** — Provide context for next session

**CRITICAL RULES:**

- Work is NOT complete until `git push` succeeds
- NEVER stop before pushing — that leaves work stranded locally
- NEVER say "ready to push when you are" — YOU must push
- If push fails, resolve and retry until it succeeds
