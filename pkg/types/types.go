package types

import (
	"time"
)

const GOOGLE_SERVICE_KEY_SECRET = "GoogleServiceKey"
const DEFAULT_GOOGLE_FOLDER_LOCATIONS_SECRET = "DefaultGoogleFolderLocations"

type DefaultGoogleFolderLocations struct {
	FolderID        string `json:"FolderID"`
	ArchiveFolderID string `json:"ArchiveFolderID"`
	DestFolderID    string `json:"DestFolderID"`
}

type Document struct {
	ID           string    `json:"id"`
	FolderID     string    `json:"folder_id"`
	Name         string    `json:"name"`
	CreatedTime  time.Time `json:"created_time"`
	ModifiedTime time.Time `json:"modified_time"`
}

// WatchChannel represents a folder location to watch for new files to process.
// When a file is detected it is processed then moved to the ArchiveFolderID.
// The results of the processing are saved to the DestinationFolderID.
//
// The ChannelID, ExpiresAt, and WebhookUrl are used to track the Google Drive
// resource that monitors the folder identified in FolderID.
type WatchChannel struct {
	FolderID            string `json:"folder_id"`
	ArchiveFolderID     string `json:"archive_folder_id"`
	DestinationFolderID string `json:"destination_folder_id"`
	ChannelID           string `json:"chanel_id"`
	ExpiresAt           int64  `json:"expires_at"`
	WebhookUrl          string `json:"webhook_url"`
}
