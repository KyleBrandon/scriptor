package main

import (
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
)

type registerConfig struct {
	store      database.ScriptorStore
	dc         *google.GoogleDriveContext
	webhookURL string
}

func main() {
	slog.Debug(">>RegisterWebhook.main")
	defer slog.Debug("<<RegisterWebhook.main")

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

	cfg := &registerConfig{
		store,
		dc,
		webhookURL}

	lambda.Start(func(request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
		slog.Debug(">>RegisterWebhook.lambda")
		defer slog.Debug("<<RegisterWebhook.lambda")

		err = cfg.seedWatchChannels()
		if err != nil {
			slog.Error("Failed to add initial watch channel", "error", err)
		}

		err := cfg.dc.ReRegisterWebhook(cfg.webhookURL)
		if err != nil {
			message := fmt.Sprintf("failed to re-register webhook: %v", err)
			return util.BuildGatewayResponse(message, http.StatusOK)
		}

		return util.BuildGatewayResponse("successfully re-regisered webhook", http.StatusOK)
	})
}

func (cfg *registerConfig) seedWatchChannels() error {
	slog.Debug(">>seedWatchChannels")
	defer slog.Debug("<<seedWatchChannels")

	// get all the watch channels
	existing, err := cfg.store.GetWatchChannels()
	if err != nil {
		return err
	}

	// do we have any watch channels configured
	if len(existing) != 0 {
		slog.Debug("No need to seed a watch channel, already configured")
		return nil
	}

	folderLocations, err := util.GetDefaultFolderLocations()
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
