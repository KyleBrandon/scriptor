# Post-Upload Email Delivery Design

## Objective

Add a new post-upload workflow step that runs after the existing Google Drive upload completes. This step will email the original PDF and the final generated Markdown file as attachments to one or more configured recipient addresses.

The initial version should be simple, observable, and production-safe:

- Google Drive upload remains the existing delivery mechanism.
- Email delivery is added as a separate workflow stage after upload.
- Recipient configuration is global and stored in AWS Secrets Manager.
- The sender identity uses the `brander-tech.com` domain through Amazon SES.
- The system sends the original PDF and the final Markdown as attachments.

## Why This Shape

The current workflow already ends cleanly at the `uploaded` stage. Email should not be hidden inside the Google Drive upload Lambda because that would combine two different delivery concerns into one step and make failures harder to reason about.

Adding a separate `emailed` stage gives us:

- independent status tracking in DynamoDB
- independent retries for email delivery
- clearer CloudWatch logs and Step Functions history
- a clean rollback path if email needs to be disabled

## Current Architecture Baseline

Today the document workflow is:

`new -> downloaded -> mathpix -> openai -> uploaded`

The new target workflow becomes:

`new -> downloaded -> mathpix -> openai -> uploaded -> emailed`

For Kindle email ingestion, the workflow continues to start at `downloaded`.

The new email step will read:

- the original PDF from the `downloaded` stage in S3
- the final Markdown from the `openai` stage in S3

This is the most reliable source of truth because both artifacts already exist in the staging bucket and are tracked in DynamoDB.

## Functional Requirements

The post-upload email step must:

- run only after the Google Drive upload step completes successfully
- load its configuration from Secrets Manager
- send to one or more configured email addresses
- attach the original PDF
- attach the final Markdown
- include a small human-readable email body
- record success and failure as a workflow stage
- fail clearly when configuration is missing or invalid
- fail clearly when the message is too large to send through SES

## Non-Goals For V1

The first version should not try to solve all future routing needs.

Out of scope for V1:

- per-folder recipient routing
- per-source recipient routing
- custom HTML email templates
- bounce and complaint processing workflows
- emailing download links instead of attachments
- custom MAIL FROM configuration
- end-user self-service configuration UI

## Recommended Default Behavior

Use these defaults for the first release:

- sender: `scriptor@brander-tech.com`
- subject: `[Scriptor] <document name>`
- body: short plain-text summary
- recipients: one global list configured in Secrets Manager
- workflow behavior: if email sending fails, fail the workflow step

Failing the step is the right default early on because it makes configuration and deliverability issues visible immediately.

## Email Content Design

### Subject

Use:

`[Scriptor] <document name>`

Example:

`[Scriptor] My Research Notes`

### Body

Use a plain-text body for the initial version:

```text
Scriptor finished processing a document.

Document: My Research Notes.pdf
Source: google_drive
Completed: 2026-03-20T15:04:05Z

Attachments:
- Original PDF
- Final Markdown
```

This keeps implementation small and avoids HTML rendering issues.

### Attachments

Attach:

- `<base-name>.pdf`
- `<base-name>.md`

The filenames should be stable and derived from the original document name.

Content types:

- PDF: `application/pdf`
- Markdown: `text/markdown; charset=utf-8`

## Delivery Provider Choice

Use Amazon SES.

Reasons:

- already aligned with the existing AWS deployment model
- already used indirectly for inbound email ingestion patterns in this project
- no separate vendor or SMTP hosting needed
- domain verification and DKIM are standard and manageable
- supported by the AWS SDK for Go v2

The `brander-tech.com` website host is not the important dependency. DNS control for the domain is the important dependency.

## Domain Management Console Steps

These are the steps you need to do wherever DNS for `brander-tech.com` is managed.

### 1. Confirm DNS Control

Make sure you can edit the DNS zone for `brander-tech.com`.

If DNS is managed by your registrar or web host, that is where you will make the record changes. If DNS is managed in Route 53, those changes may be made from AWS directly.

### 2. Create The SES DKIM Records

After creating the SES domain identity in AWS, SES will provide 3 DKIM CNAME records. Add those records exactly as given.

Important notes:

- do not remove `_domainkey`
- do not change the values
- some DNS providers want the full hostname
- some DNS providers want only the relative subdomain

Example:

If SES gives a name like:

`abc123def456._domainkey.brander-tech.com`

some providers want that full value, while others want only:

`abc123def456._domainkey`

### 3. Optional SPF Review

If you already have an SPF TXT record for `brander-tech.com`, review it instead of creating a duplicate record.

There must only be one SPF TXT record per domain.

### 4. Optional DMARC Setup

If there is no DMARC record yet, add a monitoring policy first:

`v=DMARC1; p=none; rua=mailto:dmarc@brander-tech.com`

This is optional for the first launch but recommended.

### 5. Wait For Propagation

DNS propagation can take time. SES verification often completes much faster than the maximum window, but allow time for it to settle.

## AWS Console Steps

### 1. Pick The SES Region

SES identities and sandbox status are regional. Use the same AWS region as the Scriptor deployment if possible.

### 2. Create The SES Domain Identity

In AWS Console:

1. Open Amazon SES.
2. Switch to the intended region.
3. Go to `Configuration -> Verified identities`.
4. Click `Create identity`.
5. Choose `Domain`.
6. Enter `brander-tech.com`.
7. Leave configuration set assignment empty for now.
8. Choose `Easy DKIM`.
9. Use `2048-bit` DKIM keys.
10. Create the identity.

After creation:

1. Open the new `brander-tech.com` identity.
2. Go to its authentication or DNS record section.
3. Copy the 3 DKIM CNAME records.
4. Add those records in the domain management console.
5. Return later and verify SES shows:
   - identity status: `Verified`
   - DKIM status: `Successful`

### 3. Choose The Sender Address

Create and standardize on one sender address:

- `scriptor@brander-tech.com`

Do not use a personal mailbox as the sender for the system.

### 4. Check SES Sandbox Status

If the SES account is still in sandbox for that region, request production access.

Sandbox limits are not appropriate for normal outbound delivery because sending is restricted to verified recipients and the account has lower quotas.

### 5. Create The Outbound Email Secret

Create a new AWS Secrets Manager secret:

`scriptor/post-upload-email`

Use secret type:

`Other type of secret`

Store JSON like:

```json
{
  "enabled": true,
  "from_address": "scriptor@brander-tech.com",
  "to_addresses": ["you@example.com"],
  "reply_to": ["you@example.com"],
  "subject_prefix": "[Scriptor]"
}
```

## Secret Design

This is the only required application secret for outbound email delivery.

### Secret Name

`scriptor/post-upload-email`

### Secret Schema

```json
{
  "enabled": true,
  "from_address": "scriptor@brander-tech.com",
  "to_addresses": ["ops@example.com"],
  "reply_to": ["ops@example.com"],
  "subject_prefix": "[Scriptor]"
}
```

### Field Definitions

- `enabled`
  - boolean
  - if `false`, the Lambda should skip sending and mark the email stage complete

- `from_address`
  - string
  - must be a valid address under the verified SES identity

- `to_addresses`
  - array of strings
  - must contain at least one address when `enabled` is `true`

- `reply_to`
  - array of strings
  - optional

- `subject_prefix`
  - string
  - optional
  - default to `[Scriptor]` when empty

## Code Changes

### 1. Shared Types

Update `pkg/types/types.go`.

Add:

- `POST_UPLOAD_EMAIL_SECRETS = "scriptor/post-upload-email"`
- `DOCUMENT_STAGE_EMAIL = "emailed"`

Add a new config struct:

```go
type PostUploadEmailConfig struct {
    Enabled       bool     `json:"enabled"`
    FromAddress   string   `json:"from_address"`
    ToAddresses   []string `json:"to_addresses"`
    ReplyTo       []string `json:"reply_to"`
    SubjectPrefix string   `json:"subject_prefix"`
}
```

### 2. Shared Secret Loader

Update `lambdas/util/util.go`.

Add a loader:

`LoadPostUploadEmailConfig(ctx context.Context, awsCfg aws.Config) (*types.PostUploadEmailConfig, error)`

Implementation should follow the same pattern as:

- `GetDefaultFolderLocations`
- `LoadMathpixSecrets`

Behavior:

- fetch secret by `types.POST_UPLOAD_EMAIL_SECRETS`
- unmarshal JSON
- validate minimum required fields when enabled
- default `SubjectPrefix` to `[Scriptor]` if empty

### 3. New Lambda

Create:

`lambdas/workflow_email_send/main.go`

This Lambda should:

1. initialize once using `sync.Once`
2. load AWS config
3. create S3 client
4. create SES v2 client
5. create document store
6. load post-upload email config
7. read the document record
8. read the `downloaded` stage
9. read the `openai` stage
10. start the `emailed` processing stage
11. load both artifacts from S3
12. build the outbound email payload
13. send through SES
14. mark the stage complete
15. return the unchanged `types.DocumentStep` payload

### 4. Lambda Behavior Details

The Lambda should use the existing `types.DocumentStep` input shape.

It should expect to be invoked after `uploaded`, but it should fetch artifacts using:

- `types.DOCUMENT_STAGE_DOWNLOAD`
- `types.DOCUMENT_STAGE_OPENAI`

That ensures the correct source files are attached regardless of how the upload step works.

### 5. SES Sending Approach

Use the AWS SDK for Go v2 SES v2 client.

Preferred API:

- `sesv2.SendEmail`

Use a raw or simple attachment-capable message shape depending on the SDK support that best fits the existing Go version and repo dependencies.

If attachment support in the chosen SES v2 API shape is awkward, use a standards-compliant MIME message with the SES raw email API.

The implementation should:

- set `FromEmailAddress`
- set destination recipients from config
- include plain-text body
- attach PDF bytes
- attach Markdown bytes

### 6. Attachment Size Guardrail

SES enforces a maximum total message size. Before sending:

- calculate the combined attachment and message size
- fail with a clear log message if it is too large

The error should identify:

- document ID
- original filename
- approximate total payload size

Do not truncate or partially send.

### 7. Filename Handling

Derive `baseName` from the original document name using the existing utility approach.

Send:

- `<baseName>.pdf`
- `<baseName>.md`

This avoids timestamped processing filenames leaking into the recipient experience.

### 8. Logging

Log:

- document ID
- source type
- destination recipient count
- success/failure

Do not log:

- secret JSON
- raw attachment bytes
- full MIME payload

## CDK Changes

### 1. Add Secret Reference

Update `cdk/stacks/props.go`.

Add a field:

`PostUploadEmailSecrets awssecretsmanager.ISecret`

### 2. Initialize Secret

Update `cdk/stacks/aws_resources.go`.

In `initializeSecretsManager`, add:

`cfg.PostUploadEmailSecrets = awssecretsmanager.Secret_FromSecretNameV2(...)`

using:

`types.POST_UPLOAD_EMAIL_SECRETS`

### 3. Add Lambda Packaging And Permissions

Update `cdk/stacks/document_workflow.go`.

Add:

- `configureEmailLambda(stack awscdk.Stack) awslambda.Function`

Grant the new Lambda:

- read access to `cfg.PostUploadEmailSecrets`
- read access to the document staging bucket
- read/write access to the document table if needed
- read/write access to the document processing stage table

The email step only needs S3 read access, but using `GrantReadWriteData` on the stage table is consistent with the other workflow Lambdas.

### 4. Add Step Functions Task

In `configureStateMachine`, create:

- `emailTaskFromNew`
- `emailTaskFromDownloaded`

Append them after upload:

- `download -> mathpix -> openai -> upload -> email`
- `mathpix -> openai -> upload -> email`

The new terminal stage should be `emailed`.

### 5. Timeout

Use the same task timeout pattern as the other workflow tasks unless testing shows SES delivery needs something different.

## Makefile Changes

Update `Makefile`.

Add:

- `workflow_email_send`

to `LAMBDA_NAMES`

That ensures `make all` builds and zips the new Lambda.

## README Changes

Update `README.md` to document:

- the new `emailed` stage
- the new `scriptor/post-upload-email` secret
- SES/domain verification requirements
- the fact that the final delivery now includes Google Drive plus optional email

## Data Flow

### Input

The Lambda input remains:

```json
{
  "notification_id": "abc",
  "id": "document-id",
  "stage": "uploaded"
}
```

### Processing

The email Lambda will:

1. load document metadata from DynamoDB
2. load the `downloaded` stage to find the original PDF in S3
3. load the `openai` stage to find the final Markdown in S3
4. start the `emailed` stage in DynamoDB
5. fetch both objects from S3
6. build and send the email
7. complete the `emailed` stage

### Output

Return the same shape as other workflow steps:

```json
{
  "notification_id": "abc",
  "id": "document-id",
  "stage": "emailed"
}
```

If the existing workflow Lambdas return the prior event object unchanged, follow that same pattern for consistency.

## Error Handling

### Configuration Errors

Fail when:

- secret does not exist
- secret JSON is invalid
- `enabled` is `true` and `from_address` is empty
- `enabled` is `true` and `to_addresses` is empty

### Delivery Errors

Fail when:

- SES returns a send error
- attachment payload is too large
- required S3 objects are missing
- required stage records are missing

### Disabled Mode

If `enabled` is `false`:

- log that email sending is disabled
- mark the `emailed` stage complete
- return success

This allows the feature to stay wired into the workflow while operationally disabled.

## Testing Plan

### Unit Tests

Add tests for:

- post-upload email config parsing
- default subject prefix behavior
- validation of required config fields
- attachment filename generation
- plain-text body generation
- oversized payload rejection

### Integration-Level Behavior Tests

Add focused tests around:

- loading the original PDF and final Markdown from the correct stages
- stage transitions for `emailed`
- skip behavior when disabled

### Build And Verification Commands

Run:

```bash
go test ./...
make all
make cdk-diff
```

## Rollout Plan

### Phase 1

- verify SES domain
- create sender address convention
- create the secret with one internal recipient
- deploy code
- test with a small document

### Phase 2

- verify real email delivery
- confirm attachment names and content
- confirm workflow success path

### Phase 3

- add more recipients if desired
- consider V2 enhancements from `email-design-v2.md`

## Open Decisions

These decisions should be finalized before implementation:

### 1. Sender Address

Recommended:

`scriptor@brander-tech.com`

### 2. Recipient List

Recommended:

start with one or two internal recipient addresses only

### 3. Failure Semantics

Recommended:

email failure should fail the workflow step

### 4. HTML Email

Recommended:

plain-text only for V1

### 5. Routing Model

Recommended:

global recipients only for V1

## Future Enhancements

Possible later improvements:

- per-folder recipient routing
- per-document overrides
- HTML email template
- email links instead of attachments for large files
- custom MAIL FROM domain
- per-source subject/body customization

## Implementation Checklist

### Domain And AWS

- verify DNS access for `brander-tech.com`
- create SES domain identity in the correct region
- publish DKIM DNS records
- confirm SES identity is verified
- request SES production access if needed
- create `scriptor/post-upload-email` secret

### Code

- add new secret constant and stage constant
- add `PostUploadEmailConfig` struct
- add secret loader in `lambdas/util/util.go`
- add `lambdas/workflow_email_send/main.go`
- add SES dependency if required
- wire Lambda into CDK
- add secret reference in CDK
- add workflow tasks after upload
- add Makefile packaging entry
- add tests
- update README

### Verification

- `go test ./...`
- `make all`
- `make cdk-diff`
- deploy
- send a small test document through the full workflow

## File-Level Change Map

Expected files to modify:

- `pkg/types/types.go`
- `lambdas/util/util.go`
- `lambdas/workflow_email_send/main.go`
- `cdk/stacks/props.go`
- `cdk/stacks/aws_resources.go`
- `cdk/stacks/document_workflow.go`
- `Makefile`
- `README.md`

Expected tests to add:

- `lambdas/workflow_email_send/main_test.go`
- possibly `lambdas/util/util_test.go` updates if config parsing helpers are tested there

## Notes On AWS Limits

Amazon SES supports attachments, but message size limits still apply. The implementation must guard against oversized documents and fail clearly rather than silently dropping attachments.

If large attachments become common, the next iteration should switch to emailing links to artifacts rather than the artifacts themselves.
