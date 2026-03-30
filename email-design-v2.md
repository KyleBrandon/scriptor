# Post-Upload Email Delivery V2

## Objective

Define the second iteration of the post-upload email feature. V2 builds on the V1 attachment delivery flow and adds operational maturity, deliverability controls, and richer AWS integration.

V1 is documented in [email-design.md](./email-design.md). V2 assumes V1 is already working in production with:

- SES domain verification complete
- `scriptor/post-upload-email` configured
- the `emailed` workflow stage deployed
- PDF and Markdown attachments being sent successfully

## V2 Scope

The V2 design adds:

- CloudWatch alarms for Lambda and workflow failures
- SES configuration set support
- delivery event publishing for bounces and complaints
- optional SNS-based notifications for email failures
- optional custom MAIL FROM setup for stronger mail alignment
- clearer operational runbooks and rollout checks

This document focuses on the concrete DNS, AWS, and code steps needed to add those capabilities.

## V2 Outcomes

After V2 is complete, the system should:

- keep sending emails exactly as V1 does
- record SES delivery events more explicitly
- surface bounces and complaints outside normal Lambda logs
- raise alarms when email delivery logic starts failing
- have a cleaner sender reputation setup if custom MAIL FROM is enabled

## Relationship To V1

V2 does not replace the V1 outbound email secret.

The V1 application secret remains:

`scriptor/post-upload-email`

That secret still controls:

- whether email sending is enabled
- sender address
- recipient list
- reply-to list
- subject prefix

V2 adds AWS resources and optional code paths around that base behavior.

## DNS Steps For V2

These are the DNS-side steps you need to complete if you choose to enable custom MAIL FROM in V2.

If you skip custom MAIL FROM, no additional DNS changes are required beyond the V1 SES identity and DKIM records.

### 1. Decide Whether To Use Custom MAIL FROM

Recommended custom MAIL FROM subdomain:

`mail.brander-tech.com`

This is optional. V1 can run without it.

### 2. Add Custom MAIL FROM DNS Records

After configuring custom MAIL FROM in SES, SES will give you the DNS targets to publish.

You will typically need:

- one MX record for the MAIL FROM subdomain
- one TXT record for SPF alignment on the MAIL FROM subdomain

The exact values must be copied from the SES console for the region you are using.

### 3. Verify DNS Propagation

After adding the custom MAIL FROM records:

- return to the SES identity
- confirm the MAIL FROM status is healthy

Do not cut over code changes that depend on MAIL FROM until SES shows it as configured correctly.

## AWS Console Steps For V2

### 1. Confirm V1 Baseline Is Healthy

Before adding V2 features, confirm:

- SES identity `brander-tech.com` is verified
- DKIM is successful
- outbound emails are sending through the V1 flow
- the `scriptor/post-upload-email` secret is present and correct

### 2. Create CloudWatch Alarm For The Email Lambda

Create a CloudWatch alarm on the post-upload email Lambda's `Errors` metric.

Suggested first alarm:

- namespace: `AWS/Lambda`
- metric: `Errors`
- statistic: `Sum`
- period: `5 minutes`
- threshold: `>= 1`
- evaluation periods: `1`

Recommended action:

- notify an SNS topic you monitor

### 3. Create CloudWatch Alarm For State Machine Failures

Create a CloudWatch alarm on the document workflow state machine's failed execution count.

Suggested first alarm:

- namespace: `AWS/States`
- metric: `ExecutionsFailed`
- statistic: `Sum`
- period: `5 minutes`
- threshold: `>= 1`
- evaluation periods: `1`

This catches workflow failures even if you are not actively watching Step Functions execution history.

### 4. Create An SNS Topic For Alerts

Create a topic such as:

`scriptor-email-alerts`

Subscribe one or more operator email addresses.

Use this SNS topic for:

- Lambda error alarms
- Step Functions failure alarms
- optionally SES event notifications if you add them later

### 5. Create An SES Configuration Set

In Amazon SES:

1. Go to `Configuration -> Configuration sets`.
2. Click `Create configuration set`.
3. Use a name such as:
   `scriptor-email`

For the first pass, the configuration set can exist even if it has no event destination yet.

### 6. Add SES Event Destinations

Inside the SES configuration set, add event destinations for:

- bounces
- complaints
- deliveries
- sends
- rejects

For the initial V2 rollout, the simplest event destination is CloudWatch or SNS. If you want machine processing later, EventBridge is a good follow-up path.

Recommended first pass:

- send bounce and complaint events to SNS
- optionally publish aggregate metrics to CloudWatch

### 7. Optional Custom MAIL FROM Setup

If you want better envelope-from alignment:

1. Open the `brander-tech.com` SES identity.
2. Configure custom MAIL FROM.
3. Use a subdomain such as:
   `mail.brander-tech.com`
4. Copy the required DNS records.
5. Publish them in your DNS provider.
6. Confirm SES shows MAIL FROM as healthy.

### 8. Optional CloudWatch Dashboard

Create a dashboard that shows:

- Lambda errors
- Lambda invocations
- Step Functions failed executions
- SES sends
- SES deliveries
- SES bounces
- SES complaints

This is optional, but useful once the feature is being relied on regularly.

## Code Changes For V2

### 1. Add Optional SES Configuration Set Support

Extend the outbound email config model in `pkg/types/types.go` to optionally include a configuration set name.

Recommended field:

```go
ConfigurationSetName string `json:"configuration_set_name"`
```

This field should remain optional so V1 deployments continue to work unchanged.

### 2. Extend Secret Parsing

Update `lambdas/util/util.go` so the post-upload email config loader reads the optional configuration set name.

Behavior:

- if empty, send email exactly as V1 does
- if set, include the configuration set on SES sends

### 3. Pass The Configuration Set To SES

Update `lambdas/workflow_email_send/main.go` so SES send requests include the configuration set when configured.

This is the key code link between:

- the V1 outbound email secret
- the V2 SES event-publishing setup

### 4. Improve Logging Around SES Responses

Add structured logs for:

- configuration set in use
- SES message identifier if returned
- document ID
- recipient count

Do not log full recipient lists if that is too noisy, and never log secret payloads.

### 5. Optional Bounce/Complaint Handling Lambda

If SNS is used for SES event destinations and you want automated handling, add a new Lambda such as:

`lambdas/email_event_handler/main.go`

This Lambda can:

- consume SNS messages for bounce and complaint events
- write structured logs
- optionally persist event summaries later

This is optional for V2, but useful if you want more than passive alerting.

### 6. Optional Code-Level Alert Resource Wiring

If you want alarms managed in CDK instead of manually in the console, update the CDK stacks to create:

- an SNS topic for alerts
- CloudWatch alarms for the email Lambda
- CloudWatch alarms for the state machine

This is recommended if you want infrastructure-as-code coverage for ops resources.

## CDK Changes For V2

If you decide to codify the V2 operational resources, update CDK to provision them.

### Files Likely To Change

- `cdk/stacks/props.go`
- `cdk/stacks/aws_resources.go`
- `cdk/stacks/document_workflow.go`
- possibly a new stack file for monitoring resources

### Resources To Add

- SNS topic for alerts
- CloudWatch alarm for email Lambda errors
- CloudWatch alarm for Step Functions failures
- optional outputs or references for the SES configuration set name if you want it managed alongside the app

### Recommendation

For the first V2 pass, it is acceptable to create:

- SES configuration set
- event destinations
- alarms

manually in the AWS console, then move them into CDK later if they prove stable.

## Suggested V2 Secret Shape

The V1 secret remains the source of truth. V2 can optionally extend it like this:

```json
{
  "enabled": true,
  "from_address": "scriptor@brander-tech.com",
  "to_addresses": ["ops@example.com"],
  "reply_to": ["ops@example.com"],
  "subject_prefix": "[Scriptor]",
  "configuration_set_name": "scriptor-email"
}
```

This is still the same secret:

`scriptor/post-upload-email`

There is no separate required app secret for V2.

## Recommended Execution Order

### Phase 1: AWS Visibility

1. Create SNS alert topic.
2. Create Lambda error alarm.
3. Create Step Functions failure alarm.
4. Confirm alert delivery works.

### Phase 2: SES Event Publishing

1. Create SES configuration set.
2. Add bounce and complaint event destinations.
3. Update the secret with `configuration_set_name`.
4. Update code to pass the configuration set to SES.
5. Deploy and test with a controlled send.

### Phase 3: Sender Alignment

1. Configure custom MAIL FROM in SES.
2. Publish MAIL FROM DNS records.
3. Wait for verification.
4. Confirm healthy status in SES.

## Testing Plan For V2

### Manual Verification

Verify:

- alarms trigger when forced failures occur
- SNS notifications are received
- emails still send normally with the configuration set attached
- SES event destinations receive send/delivery/bounce events

### Code Verification

Run:

```bash
go test ./...
make all
make cdk-diff
```

### Failure Injection Ideas

Use controlled failures such as:

- temporarily invalid recipient configuration in a non-production environment
- a forced Lambda error path in a development stack
- a test send through the SES simulator if you decide to use it

## File-Level Change Map For V2

Core code updates:

- `pkg/types/types.go`
- `lambdas/util/util.go`
- `lambdas/workflow_email_send/main.go`

Optional AWS infrastructure-as-code updates:

- `cdk/stacks/props.go`
- `cdk/stacks/aws_resources.go`
- `cdk/stacks/document_workflow.go`

Optional new Lambda for event handling:

- `lambdas/email_event_handler/main.go`

## Summary

V1 should stay focused on reliable attachment delivery.

V2 is where we add:

- alarms
- delivery event publishing
- optional bounce and complaint handling
- optional custom MAIL FROM support

That keeps the first release small while still giving us a clear path to a more production-hardened email system.
