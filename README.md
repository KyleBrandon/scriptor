# Scriptor

## Where does the term "Scriptor" come from?

In medieval times, a scriptor was someone who copied manuscripts by hand. These individuals played a crucial role in preserving religious, philosophical, and literary works before the invention of the printing press.

## What is Scriptor?

Scriptor is a set of lambdas and step functions that are deployed to AWS. They will monitor a Google Drive folder for PDF files uploaded, convert the PDF file to Markdown, clean it up, and they upload the newly created Markdown file and the original PDF to a destination folder while archiving the original.

There are five (5) AWS Lambda functions that are used to accomplish this.

### scriptorWebhookRegisterLambda

The scriptorWebhookRegisterLambda registers a webhook with Google Drive. The lambda is configured to read the Google Drive service secret from secrets manager along with the folder location to monitor. This is then configured to be run daily to ensure that the webhook is registered. This lambda is triggered with an AWS event to execute once a day. When triggered, the lambda will check DynamoDB for a watch channel record, if missing it will create a new watch channel for the folder that will expire in 48 hours. If a channel exists, it will determine if it has expired and re-register if needed. The watch channel record in DynamoDB stores information about the watch channel that is used to verify webhook events to ensure they are valid.

### scriptorDownloadLambda

This lambda is configured behind an API Gateway and will receive the webhook notification from Google Drive. It will confirm that the notification is for a valid watch channel that was registered. If valid, the folder associated with the watch channel is queried for any new files. These are then downloaded into a S3 downloaded staging area for processing in later stages. Once the file is downloaded a new state machine is triggered with the document information.

### scriptorMathpixProcess

This lambda is the first step in the state machine and will leverage [Mathpix](https://mathpix.com). The document from the previous stage, scriptorDownloadLambda, is copied into a multi-part form and sent to the Mathpix API. The conversion status is polled and the resultant Markdown file is copied to S3. Information on the conversion and location of the markdown is sent to the next step in the state machine.

### scriptorChatGPTProcess

This lambda is used to clean up the Markdown from Mathpix. The file from Mathpix is downloaded and sent to ChatGPT with a prompt indicating it should fix any Markdown syntax errors, spelling errors, and grammatical errors. It will also remove any tendency of ChatGPT to enclose the entire document in a Markdown code block.

### scriptorUploadLambda

This final step in the state machine will upload the final ChatGPT cleaned Markdown as well as the original PDF back to Google Drive into the configured destination folder. It will move the original PDF located in the monitor folder to a configured archive folder so it does not process it again inadvertently. Once done, the state machine is complete.

## Installation

### TBD write up installing the Lambdas

### AWS Secrets Manager configuration

The following secrets need to be configured in AWS Secrets Manager. These are configured in AWS as "Other type of secret" and stored as key/value pairs.

#### `scriptor/google-service`

This contains the service key from Google Drive

You get this service key by ...

You must grant the service key permissions to the folders below

#### `scriptor/google-folder-defaults`

This contains the Google Drive folder identifiers that are used to monitor for and store the documents. The following key/value pairs must be configured:

- `folder_id`: "identifier of the folder to watch for PDF files"
- `archive_folder_id`: "identifier of the folder to archive PDF files that have been processed"
- `destination_folder_id`: "identifier of the folder to copy the PDF and Markdown conversion"

#### `scriptor/mathpix`

This contains the Mathpix App ID and App Key that are used to call the Mathpix API.

- `mathpix_app_id`: ""
- `mathpix_app_key`: ""

#### `scriptor/chatgpt`

This contains the ChatGPT API key:

- `api_key`: "<API key from ChatGPT>"
