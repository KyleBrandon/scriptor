package types

import (
	"time"
)

const (
	//
	// Secret Manager Names
	//

	// Google service secret for using the Google Drive API
	GOOGLE_SERVICE_SECRETS = "scriptor/google-service"

	// Mathpix secrets for using the Mathpix API
	MATHPIX_SECRETS = "scriptor/mathpix"

	// ChatGPT secrets for using the API
	CHATGPT_SECRETS = "scriptor/chatgpt"

	// Google Drive folder identifiers for default monitoring
	GOOGLE_FOLDER_DEFAULT_LOCATIONS_SECRETS = "scriptor/google-folder-defaults"

	// S3 bucket to store staging and final converted files
	S3_BUCKET_NAME = "scriptor-documents"

	//
	// Document stage values
	//

	// Document downloaded to S3
	DOCUMENT_STAGE_DOWNLOADED = "downloaded"

	// Document stage Mathpix
	DOCUMENT_STAGE_MATHPIX = "mathpix"

	// Document stage ChatGPT
	DOCUMENT_STAGE_CHATGPT = "chatgpt"

	// Document stage uploaded
	DOCUMENT_STAGE_UPLOAD = "uploaded"

	//
	// Document status values
	//

	DOCUMENT_STATUS_PENDING    = "pending"
	DOCUMENT_STATUS_INPROGRESS = "in-progress"
	DOCUMENT_STATUS_COMPLETE   = "complete"
	DOCUMENT_STATUS_ERROR      = "error"

	// Document in error
	DOCUMENT_ERROR = "document-error"
)

type (
	// Default locations for where to monitor for folders and where to place
	// converted documents.
	GoogleFolderDefaultLocations struct {
		FolderID        string `json:"folder_id"`
		ArchiveFolderID string `json:"archive_folder_id"`
		DestFolderID    string `json:"destination_folder_id"`
	}

	// Mathpix application ID and Key.
	MathpixSecrets struct {
		AppID  string `json:"mathpix_app_id"`
		AppKey string `json:"mathpix_app_key"`
	}

	// ChatGPT API key
	ChatGptSecrets struct {
		ApiKey string `json:"api_key"`
	}

	// WatchChannel represents a folder location to watch for new files to process.
	// When a file is detected it is processed then moved to the ArchiveFolderID.
	// The results of the processing are saved to the DestinationFolderID.
	//
	// The ChannelID, ExpiresAt, and WebhookUrl are used to track the Google Drive
	// resource that monitors the folder identified in FolderID.
	WatchChannel struct {
		FolderID            string    `dynamodbav:"folder_id"`
		ExpiresAt           int64     `dynamodbav:"expires_at"`
		ChannelID           string    `dynamodbav:"channel_id"`
		ResourceID          string    `dynamodbav:"resource_id"`
		CreatedAt           time.Time `dynamodbav:"created_at"`
		UpdatedAt           time.Time `dynamodbav:"updated_at"`
		ArchiveFolderID     string    `dynamodbav:"archive_folder_id"`
		DestinationFolderID string    `dynamodbav:"destination_folder_id"`
		WebhookUrl          string    `dynamodbav:"webhook_url"`
	}

	// Document state as it is being converted.
	Document struct {
		// ID is the partition key for the documents table
		ID             string    `dynamodbav:"id"`
		GoogleID       string    `dynamodbav:"google_id"`
		GoogleFolderID string    `dynamodbav:"folder_id"`
		Name           string    `dynamodbav:"name"`
		Size           int64     `dynamodbav:"size"`
		CreatedTime    time.Time `dynamodbav:"created_time"`
		ModifiedTime   time.Time `dynamodbav:"modified_time"`
	}

	// DocumentProcessingStage tracks the document through each stage of processing.
	DocumentProcessingStage struct {
		ID          string    `dynamodbav:"id"`
		Stage       string    `dynamodbav:"stage"`
		CreatedAt   time.Time `dynamodbav:"created_at"`
		FileName    string    `dynamodbav:"file_name"`
		S3Key       string    `dynamodbav:"s3key"`
		StageStatus string    `dynamodbav:"stage_status"`
		Metadata    string    `dynamodbav:"metadata"`
	}

	DocumentStep struct {
		ID    string `json:"id"`
		Stage string `json:"stage"`
	}
)
