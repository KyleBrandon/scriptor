package types

import (
	"time"
)

const GOOGLE_SERVICE_KEY_SECRET = "GoogleServiceKey"
const DEFAULT_GOOGLE_FOLDER_LOCATIONS_SECRET = "DefaultGoogleFolderLocations"
const S3_BUCKET_NAME = "scriptor-documents"

type (
	DefaultGoogleFolderLocations struct {
		FolderID        string `json:"FolderID"`
		ArchiveFolderID string `json:"ArchiveFolderID"`
		DestFolderID    string `json:"DestFolderID"`
	}

	Document struct {
		ID           string    `dynamodbav:"id"`
		FolderID     string    `dynamodbav:"folder_id"`
		Name         string    `dynamodbav:"name"`
		Status       string    `dynamodbav:"status"`
		CreatedTime  time.Time `dynamodbav:"created_time"`
		ModifiedTime time.Time `dynamodbav:"modified_time"`
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

	// DocumentDownload represents a document
	DocumentDownload struct {
		DocumentID          string `json:"document_id"`
		DocumentPath        string `json:"document_path"`
		MathpixDocumentPath string `json:"mathpix_document_path"`
		ChatGptDocumentPath string `json:"chatgpt_document_path"`
	}

	// Output for the DownloadLambda
	DocumentProcessInput struct {
		Documents []DocumentDownload `json:"documents"`
	}

	DocumentProcessOutput struct {
		DocumentProcessInput
	}
)
