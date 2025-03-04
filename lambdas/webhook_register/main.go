package main

import (
	"context"
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
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

type registerConfig struct {
	store          database.WatchChannelStore
	dc             *google.GoogleDriveContext
	webhookURL     string
	secretsManager *secretsmanager.Client
}

func main() {
	slog.Info(">>RegisterWebhook.main")
	defer slog.Info("<<RegisterWebhook.main")

	awsCfg, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		slog.Error("failed to load the AWS config", "error", err)
		os.Exit(1)
	}

	store, err := database.NewDynamoDBClient()
	if err != nil {
		slog.Error("Failed to configure the DynamoDB client", "error", err)
		os.Exit(1)
	}

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

	secretsManager := secretsmanager.NewFromConfig(awsCfg)

	cfg := &registerConfig{
		store,
		dc,
		webhookURL,
		secretsManager}

	lambda.Start(func(request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
		slog.Info(">>RegisterWebhook.lambda")
		defer slog.Info("<<RegisterWebhook.lambda")

		err = cfg.seedWatchChannels()
		if err != nil {
			slog.Error("Failed to add initial watch channel", "error", err)
		}

		err := cfg.dc.ReRegisterWebhook(cfg.webhookURL)
		if err != nil {
			message := fmt.Sprintf("failed to re-register webhook: %v", err)
			return util.BuildGatewayResponse(message, http.StatusOK, nil)
		}

		return util.BuildGatewayResponse("successfully re-regisered webhook", http.StatusOK, nil)
	})
}

func (cfg *registerConfig) getSecret(secretName string) (string, error) {

	input := &secretsmanager.GetSecretValueInput{SecretId: &secretName}

	result, err := cfg.secretsManager.GetSecretValue(context.TODO(), input)
	if err != nil {
		return "", err
	}

	return *result.SecretString, nil
}

func (cfg *registerConfig) seedWatchChannels() error {
	slog.Info(">>seedWatchChannels")
	defer slog.Info("<<seedWatchChannels")

	// get all the watch channels
	existing, err := cfg.store.GetWatchChannels()
	if err != nil {
		return err
	}

	// do we have any watch channels configured
	if len(existing) != 0 {
		slog.Info("No need to seed a watch channel, already configured")
		return nil
	}

	folderLocations, err := cfg.getDefaultFolderLocations()
	if err != nil {
		return err
	}

	// Create a watch channel entry in the DB
	err = cfg.store.InsertWatchChannel(types.WatchChannel{
		FolderID:            folderLocations.FolderID,
		ChannelID:           "DEFALT",
		ArchiveFolderID:     folderLocations.ArchiveFolderID,
		DestinationFolderID: folderLocations.DestFolderID,
	})
	if err != nil {
		slog.Error("Failed to create the initialze watch channel", "error", err)
		return err
	}

	return nil
}

func (cfg *registerConfig) getDefaultFolderLocations() (types.DefaultGoogleFolderLocations, error) {

	// no watch channels yet, let's seed a default
	folderInfo, err := cfg.getSecret(types.DEFAULT_GOOGLE_FOLDER_LOCATIONS_SECRET)
	if err != nil {
		slog.Error("Failed to get the default folder locations from AWS secret manager", "error", err)
		return types.DefaultGoogleFolderLocations{}, err
	}

	var folderLocations types.DefaultGoogleFolderLocations

	err = json.Unmarshal([]byte(folderInfo), &folderLocations)
	if err != nil {
		slog.Error("Failed to unmarshal default Google folder locations from secret manager", "error", err)
		return types.DefaultGoogleFolderLocations{}, err
	}

	return folderLocations, nil

}
