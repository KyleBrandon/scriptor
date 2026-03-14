# Scriptor

## Where does the term "Scriptor" come from?

In medieval times, a scriptor was someone who copied manuscripts by hand. These individuals played a crucial role in preserving religious, philosophical, and literary works before the invention of the printing press.

## What is Scriptor?

Scriptor is a set of lambdas and step functions that are deployed to AWS. They monitor supported document sources, convert PDFs to Markdown, clean them up, and upload the newly created Markdown file and the original PDF to a destination folder while archiving the original when the source supports archiving.

Scriptor currently supports Google Drive as a webhook-driven source and Kindle exports as an SES email-driven source.

### scriptorWebhookRegisterLambda

The scriptorWebhookRegisterLambda registers a webhook with Google Drive. The lambda is configured to read the Google Drive service secret from secrets manager along with the folder location to monitor. This is then configured to be run daily to ensure that the webhook is registered. This lambda is triggered with an AWS event to execute once a day. When triggered, the lambda will check DynamoDB for a watch channel record, if missing it will create a new watch channel for the folder that will expire in 48 hours. If a channel exists, it will determine if it has expired and re-register if needed. The watch channel record in DynamoDB stores information about the watch channel that is used to verify webhook events to ensure they are valid.

### scriptorDownloadLambda

This lambda is configured behind an API Gateway and will receive the webhook notification from Google Drive. It will confirm that the notification is for a valid watch channel that was registered. If valid, the folder associated with the watch channel is queried for any new files. These are then downloaded into a S3 downloaded staging area for processing in later stages. Once the file is downloaded a new state machine is triggered with the document information.

### scriptorEmailIngestLambda

This lambda is triggered from a SQS queue that receives notifications for raw SES emails stored in S3. It parses the Kindle export email, extracts the "Download PDF" link, resolves the signed S3 URL, downloads the PDF into the Scriptor staging bucket, and starts the Step Functions workflow at the `downloaded` stage.

### scriptorMathpixProcess

This lambda is the first step in the state machine and will leverage [Mathpix](https://mathpix.com). The document from the previous stage, scriptorDownloadLambda, is copied into a multi-part form and sent to the Mathpix API. The conversion status is polled and the resultant Markdown file is copied to S3. Information on the conversion and location of the markdown is sent to the next step in the state machine.

### scriptorOpenAIProcess

This lambda is used to clean up the Markdown from Mathpix. The file from Mathpix is downloaded and sent to OpenAI, along with the original PDF, so the model can correct OCR issues against the source document and return cleaned Markdown. The Lambda name is historical; the provider is now OpenAI.

### scriptorUploadLambda

This final step in the state machine will upload the final LLM-cleaned Markdown as well as the original PDF back to Google Drive into the configured destination folder. It will move the original PDF located in the monitor folder to a configured archive folder so it does not process it again inadvertently. Once done, the state machine is complete.

## Architecture and Operational Constraints

### End-to-End Processing Stages

Documents progress through these stages:

`new` -> `downloaded` -> `mathpix` -> `openai` -> `uploaded`

Each stage tracks status (`pending`, `in-progress`, `complete`, `error`) in DynamoDB.

Google Drive documents enter the workflow at `new`. Kindle email documents are staged by `scriptorEmailIngestLambda` first and then enter the workflow at `downloaded`.

### Runtime Limits and Reliability Rules

- Step Functions workflow timeout: 15 minutes total
- Per-task timeout: 3 minutes
- All timestamps are stored in UTC
- Files with the same name in the same Drive folder are de-duplicated
- Google Drive watch channels are created for 48 hours and renewed when expiry is within ~20 hours
- Watch channel locks expire to recover from interrupted Lambda executions

### Core Data and Storage Conventions

- DynamoDB tables:
  - `Documents`
  - `DocumentProcessingStage`
  - `WatchChannels`
  - `WatchChannelLocks`
- S3 object key pattern:
  - `{documentID}/{stage}/{filename}.{ext}`
  - Example: `abc123/mathpix/report.md`

### Contributor Docs

For contributor workflow, coding conventions, and PR expectations, see [`AGENTS.md`](AGENTS.md).

## Installation

### Prerequisites

- [Go](https://go.dev/dl/) 1.23 or later
- [AWS CLI](https://aws.amazon.com/cli/) configured with credentials
- [AWS CDK](https://docs.aws.amazon.com/cdk/v2/guide/getting_started.html) v2 installed (`npm install -g aws-cdk`)
- A Google Cloud project with a service account
- A Mathpix account with API access
- An OpenAI account with API access

### Building

Build all Lambda functions:

```bash
make all
```

This cross-compiles each Lambda for `linux/amd64` and packages them as zip files in `./bin/`.

### Deploying

Preview infrastructure changes, then deploy:

```bash
make cdk-diff       # Build lambdas + show CDK diff
make cdk-deploy     # Build, diff, deploy all stacks to AWS
```

### AWS Secrets Manager Configuration

The following secrets need to be configured in AWS Secrets Manager. These are configured in AWS as "Other type of secret" and stored as key/value pairs.

#### scriptor/google-folder-defaults

This contains the Google Drive folder identifiers that are used to monitor for and store the documents. In your Google Drive account create a folder called **Scriptor**. This is your Scriptor Root Folder and will be used as the root folder to contain all the documents that Scriptor works with. The following key/value pairs must be configured:

- `folder_id`: "identifier of the folder to watch for PDF files"
- `archive_folder_id`: "identifier of the folder to archive PDF files that have been processed"
- `destination_folder_id`: "identifier of the folder to copy the PDF and Markdown conversion"

#### scriptor/google-service

This contains the service key from Google Drive. In order to obtain a service key for Google Drive you will need to create a Service Account, enable the Google Drive API, and grant the Service Account Permissions to the folders that Scriptor will monitor. The steps below will walk you through creating a service account in Google Cloud to monitor the Scriptor folder. You will need to create a new secret in Secrets Manager of "Other type of secret" and copy the JSON key file from Google Cloud into the **Plaintext** section of the **Key/value pairs**.

#### Step 1: Create a Service Account in Google Cloud

1. Go to the [Google Cloud Console](https://console.cloud.google.com).
2. Select your project or create a new one.
3. Navigate to **IAM & Admin → Service Accounts**.
4. Click **Create Service Account**.
5. Enter a **name** and **description** for the service account.
6. Click **Create and Continue**
7. Assign the necessary **roles** you would like the service account to have e.g. Editor.
8. Click **Done**

#### Step 2: Generate a Service Account Key

1. In the **Service Accounts** page, find the newly created service account.
2. Click on the account to open details.
3. Navigate to the **Keys** tab.
4. Click **Add Key → Create New Key**.
5. Select **JSON** as the key type and click **Continue**.
6. A JSON file containing the credentials will be downloaded.
7. **PROTECT THIS KEY**

#### Step 3: Enable Google Drive API

1. Go to the **APIs & Services → Library**.
2. Search for **Google Drive API**.
3. Click **Enable**.

#### Step 4: Share Your Google Drive folder with the Service Account

1. Open your **Google Drive**.
2. Right-click on the folder or file you want to share.
3. Click **Share**.
4. Add the **service account's email** (e.g. file-monitor@my-new-project-12345.iam.gserviceaccount.com).
5. Assign **Editor** permissions as Scriptor will need to create and move documents.
6. Click **Done**.

#### scriptor/mathpix

This contains the Mathpix App ID and App Key that are used to call the Mathpix API.

Head to mathpix.com, hit “Try for Free” and make an account. Once you log in, select "Go to Console" to manage the API, we're not using their other note taking features. Select the menu option "Convert" at the top of the screen and create an Organization. We want the "Pay as you go" billing. There is a one time ~$20 setup fee, but they give you ~$30 in credit for testing. Once you set up your billing, you should see a section under your organization for API keys. Scriptor will want to use the "APP ID" and the "APP KEY". There is a guide for getting a Mathpix API key here https://mathpix.com/docs/convert/creating-an-api-key.

You will want to create a Secrets Manager secret titled `scriptor/mathpix` with the following key/values:

- `mathpix_app_id`: "<Your APP ID from Mathix>"
- `mathpix_app_key`: "<Your APP KEY from Mathpix>"

#### scriptor/openai

This contains the OpenAI API key used by the Markdown cleanup Lambda.

##### Getting an OpenAI API Key

1. Go to the [OpenAI Platform](https://platform.openai.com/).
2. Create an account or sign in.
3. Navigate to **API keys**.
4. Click **Create Key**.
5. Give the key a name (e.g. "scriptor") and click **Create Key**.
6. Copy the key immediately -- it will not be shown again.

##### Creating the Secret

Create a Secrets Manager secret titled `scriptor/openai` of type "Other type of secret" with the following key/value pair:

- `api_key`: "<Your API key from OpenAI>"

##### OpenAI API Pricing

Scriptor uses OpenAI for Markdown cleanup. Pricing is per-token and based on the size of each document processed. See [OpenAI pricing](https://openai.com/api/pricing/) for current rates.
