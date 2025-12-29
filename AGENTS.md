# Agent Instructions

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

This project uses **bd** (beads) for issue tracking. Run `bd onboard` to get started.

## Quick Reference

```bash
bd ready              # Find available work
bd show <id>          # View issue details
bd update <id> --status in_progress  # Claim work
bd close <id>         # Complete work
bd sync               # Sync with git
```

## Project Overview

Scriptor is an AWS-based serverless document processing system that monitors Google Drive folders for PDF files, converts them to Markdown using Mathpix and ChatGPT, and uploads the processed files back to Google Drive. The system is built with Go and AWS CDK.

## Development Commands

### Building
```bash
make all                 # Build all Lambda functions (creates .zip files in ./bin)
make lambdas            # Same as 'make all'
make clean              # Remove all generated .zip files from ./bin
```

### Deployment
```bash
make cdk-diff           # Build lambdas and show CDK deployment diff
make cdk-deploy         # Build, diff, and deploy all CDK stacks to AWS
```

Note: Lambda functions are built for Linux/AMD64 (`GOOS=linux GOARCH=amd64`) and packaged as `bootstrap` executables in zip files, which is required for AWS Lambda's `PROVIDED_AL2` runtime.

### Testing
Currently, there are no automated tests in this codebase.

## Architecture

### System Flow
1. **Webhook Registration** (`webhook_register`) - Runs daily to register/renew Google Drive webhooks for folder monitoring (48-hour expiration, renewed every 20 hours)
2. **Webhook Handler** (`webhook_handler`) - Receives Google Drive notifications via API Gateway, validates watch channels, enqueues document processing
3. **SQS Handler** (`sqs_handler`) - Processes queued notifications, queries for new files, triggers state machine
4. **Step Functions Workflow** - Sequential processing pipeline:
   - `workflow_download` - Downloads PDFs from Google Drive to S3
   - `workflow_mathpix_process` - Converts PDF to Markdown using Mathpix API
   - `workflow_chatgpt_process` - Cleans up Markdown (fixes syntax, spelling, grammar)
   - `workflow_upload` - Uploads final Markdown and original PDF to destination folder, archives original

### Key Components

**CDK Infrastructure** (`cdk/stacks/`)
- `aws_resources.go` - Shared resources (DynamoDB tables, S3 buckets, secrets, SQS queues)
- `webhook_register.go` - Daily EventBridge trigger for webhook registration
- `webhook_handler.go` - API Gateway + Lambda for receiving Google Drive notifications
- `sqs_handler.go` - SQS queue consumer Lambda
- `document_workflow.go` - Step Functions state machine definition with 4 Lambda workflow stages
- `props.go` - CDK configuration and stack properties

**Database Layer** (`pkg/database/`)
- DynamoDB abstraction with four tables:
  - `Documents` - Document metadata from Google Drive
  - `DocumentProcessingStage` - Tracks documents through workflow stages
  - `WatchChannels` - Google Drive watch channel registrations
  - `WatchChannelLocks` - Distributed locking for change token management

**Google Drive Integration** (`pkg/google/drive.go`)
- Service account authentication via AWS Secrets Manager
- Watch API for folder monitoring
- File download/upload operations
- Handles change tokens for incremental sync

**Types** (`pkg/types/types.go`)
- Defines all domain types and constants
- Secret names, S3 bucket names, document stages/statuses
- DynamoDB-marshaled structs for persistence

### Watch Channel Management
- Watch channels expire after 48 hours (Google Drive limitation)
- `webhook_register` Lambda runs daily to check/renew channels
- Channels are renewed if they expire within 20 hours
- `WatchChannelLock` table implements distributed locking to prevent duplicate change queries
- Lock expires after a configured duration to handle lambda failures

### Document Processing Stages
Documents flow through these stages (tracked in `DocumentProcessingStage` table):
1. `new` - Initial state
2. `downloaded` - PDF downloaded to S3
3. `mathpix` - Converted to Markdown by Mathpix
4. `chatgpt` - Cleaned up by ChatGPT
5. `uploaded` - Final files uploaded to Google Drive

Each stage tracks: `stage_status` (pending/in-progress/complete/error), `started_at`, `completed_at`, `s3key`, `file_name`

## AWS Configuration

### Required Secrets (AWS Secrets Manager)
All secrets are "Other type of secret" with key/value pairs:

1. `scriptor/google-service` - Google service account JSON (paste entire JSON in Plaintext section)
2. `scriptor/google-folder-defaults`:
   - `folder_id` - Google Drive folder to monitor
   - `archive_folder_id` - Where to move processed PDFs
   - `destination_folder_id` - Where to upload final Markdown and PDF
3. `scriptor/mathpix`:
   - `mathpix_app_id`
   - `mathpix_app_key`
4. `scriptor/chatgpt`:
   - `api_key`

### Google Drive Setup
- Create a Google Cloud service account with Google Drive API enabled
- Share the Scriptor folder hierarchy with the service account email (grant Editor permissions)
- Service account needs ability to watch, read, create, move files

## Code Patterns

### Lambda Initialization
All lambdas follow this pattern:
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
This ensures AWS SDK clients, database connections, and secrets are loaded once per container lifecycle.

### Database Operations
- Use `database.New*Store(ctx)` constructors to create store interfaces
- All DynamoDB operations use `dynamodbav` struct tags for marshaling
- Use `context.Context` for all database calls
- Error handling: Check for `ErrDocumentNotFound` and `ErrWatchChannelLockNotFound`

### S3 Key Patterns
Documents are stored in S3 with stage-based keys:
- Downloaded: `{documentID}/downloaded/{filename}.pdf`
- Mathpix: `{documentID}/mathpix/{filename}.md`
- ChatGPT: `{documentID}/chatgpt/{filename}.md`

### CDK Grants Pattern
When adding Lambda permissions, use CDK grant methods:
```go
cfg.GoogleServiceKeySecret.GrantRead(lambda, nil)
cfg.documentTable.GrantReadWriteData(lambda)
cfg.documentBucket.GrantReadWrite(lambda, nil)
```

## Important Notes

- **Duplicate Prevention**: Files with the same name in the same Google Drive folder are de-duplicated
- **UTC Time**: All timestamps are stored and compared in UTC
- **State Machine Timeout**: Overall workflow timeout is 15 minutes, individual tasks timeout at 3 minutes
- **Lambda Runtime**: Uses `PROVIDED_AL2` with custom `bootstrap` executable (not standard Go runtime)
- **Webhook Validation**: `webhook_handler` validates incoming notifications against registered watch channels in DynamoDB before processing

## Landing the Plane (Session Completion)

**When ending a work session**, you MUST complete ALL steps below. Work is NOT complete until `git push` succeeds.

**MANDATORY WORKFLOW:**

1. **File issues for remaining work** - Create issues for anything that needs follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **PUSH TO REMOTE** - This is MANDATORY:
   ```bash
   git pull --rebase
   bd sync
   git push
   git status  # MUST show "up to date with origin"
   ```
5. **Clean up** - Clear stashes, prune remote branches
6. **Verify** - All changes committed AND pushed
7. **Hand off** - Provide context for next session

**CRITICAL RULES:**
- Work is NOT complete until `git push` succeeds
- NEVER stop before pushing - that leaves work stranded locally
- NEVER say "ready to push when you are" - YOU must push
- If push fails, resolve and retry until it succeeds

