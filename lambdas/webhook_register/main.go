package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/KyleBrandon/scriptor/lambdas/util"
	"github.com/KyleBrandon/scriptor/pkg/database"
	"github.com/KyleBrandon/scriptor/pkg/google"
	"github.com/KyleBrandon/scriptor/pkg/types"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/secretsmanager"
)

type registerConfig struct {
	store          database.WatchChannelStore
	dc             *google.GoogleDriveContext
	webhookURL     string
	secretsManager *secretsmanager.SecretsManager
}

func main() {
	slog.Info(">>RegisterWebhook.main")
	defer slog.Info("<<RegisterWebhook.main")

	store := database.NewDynamoDBClient()
	dc, err := google.NewGoogleDrive(store)
	if err != nil {
		//
		slog.Error("failed to initialize the Google Drive service context", "error", err)
		os.Exit(1)
	}

	webhookURL := os.Getenv("WEBHOOK_URL")
	if webhookURL == "" {
		slog.Error("webhook URL not configured")
		os.Exit(1)
	}

	sess := session.Must(session.NewSession())
	secretsManager := secretsmanager.New(sess)

	cfg := registerConfig{
		store,
		dc,
		webhookURL,
		secretsManager}

	err = cfg.seedWatchChannels()
	if err != nil {
		slog.Error("Failed to add initial watch channel", "error", err)
	}

	lambda.Start(func(request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
		slog.Info(">>RegisterWebhook.lambda")
		defer slog.Info("<<RegisterWebhook.lambda")

		err := cfg.dc.ReRegisterWebhook(cfg.webhookURL)
		if err != nil {
			message := fmt.Sprintf("failed to re-register webhook: %v", err)
			return util.BuildGatewayResponse(message, http.StatusOK, nil)
		}

		return util.BuildGatewayResponse("successfully re-regisered webhook", http.StatusOK, nil)
	})
}

func (cfg registerConfig) getSecret(secretName string) (string, error) {

	input := &secretsmanager.GetSecretValueInput{SecretId: &secretName}

	result, err := cfg.secretsManager.GetSecretValue(input)
	if err != nil {
		return "", err
	}

	return *result.SecretString, nil
}

func (cfg registerConfig) seedWatchChannels() error {
	existing, err := cfg.store.GetWatchChannels()
	if err != nil {
		return err
	}

	// do we have any watch channels configured
	if len(existing) == 0 {
		// No watch channels found so we should seed it with one

		folderInfo, err := cfg.getSecret(types.DEFAULT_GOOGLE_FOLDER_LOCATIONS_SECRET)
		if err != nil {
			return err
		}

		var folderLocations types.DefaultGoogleFolderLocations

		err = json.Unmarshal([]byte(folderInfo), &folderLocations)
		if err != nil {
			slog.Error("Failed to unmarshal default Google folder locations from secret manager", "error", err)
			return err
		}

		// Create a watch channel entry in the DB
		err = cfg.store.InsertWatchChannel(types.WatchChannel{
			FolderID:            folderLocations.FolderID,
			ArchiveFolderID:     folderLocations.ArchiveFolderID,
			DestinationFolderID: folderLocations.DestFolderID,
		})
		if err != nil {
			return fmt.Errorf("failed to create the initialze watch channel")
		}

	}

	return nil
}
