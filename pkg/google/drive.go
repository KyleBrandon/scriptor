package google

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/KyleBrandon/scriptor/pkg/types"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

type GoogleDriveContext struct {
	driveService *drive.Service
}

// Create a new Google Drive storage context
func NewGoogleDrive() (*GoogleDriveContext, error) {
	slog.Debug(">>GDriveStorageContext.New")
	defer slog.Debug("<<GDriveStorageContext.New")

	drive := &GoogleDriveContext{}
	service, err := drive.getDriveService()
	if err != nil {
		return nil, err
	}

	drive.driveService = service

	return drive, nil
}

func getGoogleCredentials() ([]byte, error) {
	awsCfg, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		slog.Error("failed to load the AWS config", "error", err)
		os.Exit(1)
	}

	svc := secretsmanager.NewFromConfig(awsCfg)

	secretName := types.GOOGLE_SERVICE_SECRETS
	input := &secretsmanager.GetSecretValueInput{SecretId: &secretName}

	result, err := svc.GetSecretValue(context.TODO(), input)
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

func (gd *GoogleDriveContext) GetChangesStartToken() (string, error) {

	slog.Debug(">>GetChangesStartToken")
	defer slog.Debug("<<GetChangesStartToken")

	resp, err := gd.driveService.Changes.GetStartPageToken().Do()
	if err != nil {
		slog.Error("Failed to query the changes start token", "error", err)
		return "", err
	}

	return resp.StartPageToken, nil
}

func (gd *GoogleDriveContext) QueryChanges(folderID, startToken string) (*types.DocumentChanges, error) {
	slog.Debug(">>QueryChanges")
	defer slog.Debug("<<QueryChanges")

	documents := make([]*types.Document, 0)
	pageToken := startToken

	for pageToken != "" {

		// get the changes since the pageToken
		changes, err := gd.driveService.Changes.List(pageToken).Fields("newStartPageToken, changes(file(id, name, parents, createdTime, modifiedTime, size))").Do()
		if err != nil {
			slog.Error("Failed to query the drive changes using a start token", "folderID", folderID, "startToken", startToken, "error", err)
			return nil, err
		}

		// build a Document from each file that's changed
		for _, change := range changes.Changes {

			// ignore drive changes
			if change.ChangeType == "drive" {
				continue
			}

			document, err := buildDocument(change.File)
			if err != nil {
				slog.Error("Failed to build the document from the Google Drive File", "docName", change.File.Name, "error", err)
				continue
			}

			documents = append(documents, document)
		}

		if changes.NewStartPageToken != "" {
			startToken = changes.NewStartPageToken
		}

		pageToken = changes.NextPageToken
	}

	dc := &types.DocumentChanges{
		Documents:      documents,
		NextStartToken: startToken,
	}

	return dc, nil
}

func (gd *GoogleDriveContext) GetDocument(id string) (*types.Document, error) {
	slog.Debug(">>GetDocument")
	defer slog.Debug("<<GetDocument")

	file, err := gd.driveService.Files.Get(id).Fields("id, name, parents, createdTime, modifiedTime, size").Do()
	if err != nil {
		slog.Error("Failed to get document by ID", "id", id, "error", err)
		return nil, err
	}

	document, err := buildDocument(file)
	if err != nil {
		slog.Error("Failed to parse document", "id", id, "error", err)
		return nil, err
	}

	return document, nil
}

func buildDocument(file *drive.File) (*types.Document, error) {

	createdTime, err := time.Parse(time.RFC3339, file.CreatedTime)
	if err != nil {
		slog.Warn("Failed to parse the created time for the file", "fileID", file.Id, "fileName", file.Name, "createdTime", file.CreatedTime, "error", err)
		return nil, err
	}

	modifiedTime, err := time.Parse(time.RFC3339, file.ModifiedTime)
	if err != nil {
		slog.Warn("Failed to parse the modified time for the file", "fileID", file.Id, "fileName", file.Name, "modifiedTime", file.ModifiedTime, "error", err)
		return nil, err
	}

	document := &types.Document{
		ID:             uuid.New().String(),
		GoogleID:       file.Id,
		GoogleFolderID: file.Parents[0],
		Name:           file.Name,
		Size:           file.Size,
		CreatedTime:    createdTime,
		ModifiedTime:   modifiedTime,
	}

	return document, nil

}

func (gd *GoogleDriveContext) Archive(id string, archiveFolderID string) error {
	// 	// move the document to the archive folder
	file, err := gd.driveService.Files.Get(id).Fields("parents").Do()
	if err != nil {
		return err
	}

	previousParents := strings.Join(file.Parents, ",")
	_, err = gd.driveService.Files.Update(id, nil).
		AddParents(archiveFolderID).
		RemoveParents(previousParents).
		Fields("id, parents").
		Do()
	if err != nil {
		return err
	}

	return nil
}

// Get a io.Reader for the document
func (gd *GoogleDriveContext) GetReader(document *types.Document) (io.ReadCloser, error) {
	// Get the file data
	resp, err := gd.driveService.Files.Get(document.GoogleID).Download()
	if err != nil {
		slog.Error("Unable to get the file reader", "GoogleID", document.GoogleID, "error", err)
		return nil, err

	}

	return resp.Body, nil
}

// Save a file to a Google Drive folder location
func (gd *GoogleDriveContext) SaveFile(fileName, folderID string, reader io.Reader) error {
	// Define file metadata (including folder destination)
	fileMetadata := &drive.File{
		Name:    fileName,
		Parents: []string{folderID}, // Upload to specific folder
	}

	// Upload the file
	_, err := gd.driveService.Files.Create(fileMetadata).
		Media(reader).
		Do()

	if err != nil {
		return fmt.Errorf("unable to upload file: %w", err)
	}

	return nil
}

func (gd *GoogleDriveContext) CreateWatchChannel(wc *types.WatchChannel, url string) error {
	slog.Debug(">>createWatchChannel")
	defer slog.Debug("<<createWatchChannel")

	// Set the watch channel to expire in 2 days
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
	channel, err := gd.driveService.Files.Watch(wc.FolderID, req).Do()
	if err != nil {
		slog.Error("Failed to watch folder", "folderID", wc.FolderID, "error", err)
		return err
	}

	// save the resource identifier from AWS for the channel
	wc.ResourceID = channel.ResourceId

	return nil
}
