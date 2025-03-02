package google

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"time"

	"github.com/KyleBrandon/scriptor/pkg/database"
	"github.com/KyleBrandon/scriptor/pkg/types"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/secretsmanager"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

type GoogleDriveContext struct {
	driveService *drive.Service
	store        database.WatchChannelStore
}

// Create a new Google Drive storage context
func NewGoogleDrive(store database.WatchChannelStore) (*GoogleDriveContext, error) {
	slog.Debug(">>GDriveStorageContext.New")
	defer slog.Debug("<<GDriveStorageContext.New")

	drive := &GoogleDriveContext{}
	service, err := drive.getDriveService()
	if err != nil {
		return nil, err
	}

	drive.driveService = service
	drive.store = store

	return drive, nil
}

func getGoogleCredentials() ([]byte, error) {
	sess := session.Must(session.NewSession())
	svc := secretsmanager.New(sess)

	secretName := types.GOOGLE_SERVICE_KEY_SECRET
	input := &secretsmanager.GetSecretValueInput{SecretId: &secretName}

	result, err := svc.GetSecretValue(input)
	if err != nil {
		return nil, err
	}

	return []byte(*result.SecretString), nil
}

func (gd *GoogleDriveContext) getDriveService() (*drive.Service, error) {
	// Load service account JSON
	data, err := getGoogleCredentials()
	if err != nil {
		slog.Error("Unable to read service account file", "error", err)
		return nil, err
	}

	// Authenticate with Google Drive API using Service Account
	creds, err := google.CredentialsFromJSON(context.TODO(), data, drive.DriveScope)
	if err != nil {
		slog.Error("Unable to parse credentials", "error", err)
		return nil, err
	}

	// Create an HTTP client using TokenSource
	client := oauth2.NewClient(context.TODO(), creds.TokenSource)

	// Create Google Drive service
	service, err := drive.NewService(context.TODO(), option.WithHTTPClient(client))
	if err != nil {
		slog.Error("Unable to create Drive client", "error", err)
		return nil, err
	}

	return service, nil
}

// QueryFiles from the watch folder and send them on the channel
// TODO: Add wait group and process files in Go Routine
// TODO: send files all at once instead of one at a time
func (gd *GoogleDriveContext) QueryFiles() {
	slog.Debug(">>GoogleDrive.checkForNewOrModifiedFiles")
	defer slog.Debug("<<GoogleDrive.checkForNewOrModifiedFiles")

	// build the query string to find the new fines in Google Drive
	query := gd.buildFileSearchQuery()

	fileList, err := gd.driveService.Files.List().Q(query).Fields("files(id, name, parents, createdTime, modifiedTime)").Do()
	if err != nil {
		slog.Error("Failed to fetch files", "error", err)
		return
	}

	if len(fileList.Files) == 0 {
		slog.Debug("No files found.")
		return
	}

	slog.Debug("GDriveStorage process file list", "file Count", len(fileList.Files))
	for _, file := range fileList.Files {
		slog.Debug("File:", "fileName", file.Name, "driveID", file.DriveId, "fileID", file.Id, "createdTime", file.CreatedTime, "modifiedTime", file.ModifiedTime)

		// createdTime, err := time.Parse(time.RFC3339, file.CreatedTime)
		// if err != nil {
		// 	slog.Warn("Failed to parse the created time for the file", "fileID", file.Id, "fileName", file.Name, "createdTime", file.CreatedTime, "error", err)
		// }

		// modifiedTime, err := time.Parse(time.RFC3339, file.ModifiedTime)
		// if err != nil {
		// 	slog.Warn("Failed to parse the modified time for the file", "fileID", file.Id, "fileName", file.Name, "modifiedTime", file.ModifiedTime, "error", err)
		// }

		// document := Document{
		// 	StorageDocumentID: file.Id,
		// 	StorageFolderID:   file.Parents[0],
		// 	Name:              file.Name,
		// 	CreatedTime:       createdTime,
		// 	ModifiedTime:      modifiedTime,
		// }
	}
}

// Write the given file to the storage
func (gd *GoogleDriveContext) Write(srcDoc *types.Document, reader io.ReadCloser) (*types.Document, error) {
	defer reader.Close()
	return &types.Document{}, errors.ErrUnsupported
}

// Get a io.Reader for the document
func (gd *GoogleDriveContext) GetReader(document *types.Document) (io.ReadCloser, error) {
	// Get the file data
	resp, err := gd.driveService.Files.Get(document.ID).Download()
	if err != nil {
		slog.Error("Unable to get the file reader", "error", err)
		return nil, err

	}

	return resp.Body, nil
}

// func (gd *GoogleDriveContext) watchChannelExists(channelID string) bool {
// 	for _, v := range gd.channelWatchMap {
// 		if v.ChannelID == channelID {
// 			return true
// 		}
// 	}

// 	return false
// }

func (gd *GoogleDriveContext) buildFileSearchQuery() string {
	query := "mimeType='application/pdf' and ("

	// index := 0
	// for k := range gd.channelWatchMap {
	// 	if index != 0 {
	// 		query = query + " or "
	// 	}
	// 	query = fmt.Sprintf("%s'%s' in parents", query, k)
	// 	index++
	// }

	// query = query + ")"

	return query
}

// func (gd *GoogleDriveContext) stopChannelWatch(channelID, resourceID string) {
// 	ch := &drive.Channel{
// 		Id:         channelID,
// 		ResourceId: resourceID,
// 	}

// 	// Stop watching the channel
// 	gd.driveService.Channels.Stop(ch).Do()
// }

func (gd *GoogleDriveContext) ReRegisterWebhook(url string) error {
	slog.Info(">>ReRegisterWebhook")
	defer slog.Info("<<ReRegisterWebhook")

	// get all the channels that we're currently watching
	watchChannels, err := gd.store.GetWatchChannels()
	if err != nil {
		slog.Error("Failed to get the list of active watch channels", "error", err)
		return err
	}

	if len(watchChannels) == 0 {
		slog.Warn("There are no watch channels configured")
		return nil
	}

	register := make([]types.WatchChannel, 0)

	// look for any channels that have expired
	channelRegisterTime := time.Now().Add(-1 * time.Hour).UnixMilli()
	for _, wc := range watchChannels {
		if wc.ExpiresAt <= channelRegisterTime || wc.WebhookUrl != url {
			// we need to re-register this channel
			register = append(register, wc)
		}
	}

	if len(register) == 0 {
		slog.Debug("No watch channels require re-registration")
		return nil
	}

	for _, wc := range register {
		err = gd.createWatchChannel(wc, url)
		if err != nil {
			slog.Error("failed to register channel for folder", "folderID", wc.FolderID, "channelID", wc.ChannelID, "error", err)
		}
	}

	return nil
}

func (gd *GoogleDriveContext) createWatchChannel(wc types.WatchChannel, url string) error {
	slog.Info(">>createWatchChannel")
	defer slog.Info("<<createWatchChannel")

	wc.ChannelID = uuid.New().String()
	wc.ExpiresAt = time.Now().Add(48 * time.Hour).UnixMilli()
	wc.WebhookUrl = url

	req := &drive.Channel{
		Id:         wc.ChannelID,
		Type:       "web_hook",
		Address:    wc.WebhookUrl,
		Expiration: wc.ExpiresAt,
	}

	// Watch for changes in the folder
	_, err := gd.driveService.Files.Watch(wc.FolderID, req).Do()
	if err != nil {
		slog.Error("Failed to watch folder", "resourceID", wc.FolderID, "error", err)
		return nil
	}

	err = gd.store.UpdateWatchChannel(wc)
	if err != nil {
		slog.Error("Failed to create or update the watch channel", "folderID", wc.FolderID, "channelID", wc.ChannelID, "error", err)
		return err
	}

	return nil
}
