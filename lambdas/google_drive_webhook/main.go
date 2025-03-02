package main

import (
	"log/slog"
	"net/http"
	"os"

	"github.com/KyleBrandon/scriptor/pkg/database"
	"github.com/KyleBrandon/scriptor/pkg/google"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
)

func NewFile(drive *google.GoogleDriveContext, request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
	slog.Info(">>NewFile")
	defer slog.Info("<<NewFile")

	// Extract headers sent by Google Drive
	resourceState := request.Headers["X-Goog-Resource-State"]
	channelID := request.Headers["X-Goog-Channel-ID"]
	resourceID := request.Headers["X-Goog-Resource-ID"]

	// // did we receive a notification for an old channel?
	// if !gd.watchChannelExists(channelID) {
	// 	slog.Error("watch channel does not exist", "channelID", channelID)
	// 	gd.stopChannelWatch(channelID, resourceID)
	// 	return
	// }

	// If we receive a 'sync' notification, ignore it for now.
	// We could use this for initialzing the state of the vault?
	if resourceState != "add" {
		slog.Debug("Webhook received non-add resource state", "channelID", channelID, "resourceID", resourceID, "resourceState", resourceState)
		return events.APIGatewayProxyResponse{
			Body:       "Only interested in new files that are 'add'ed",
			StatusCode: http.StatusOK,
		}, nil
	}

	// Check for new or modified files
	// drive.QueryFiles()
	slog.Info("File Received", "channelID", channelID, "resourceID", resourceID, "resourceState", resourceState)

	return events.APIGatewayProxyResponse{
		Body:       "Processing new file",
		StatusCode: http.StatusOK,
	}, nil
}

func main() {
	slog.Info(">>GoogleDriveWebhook.main")
	defer slog.Info("<<GoogleDriveWebhook.main")

	store, err := database.NewDynamoDBClient()
	if err != nil {
		slog.Error("Failed to configure the DynamoDB client", "error", err)
		os.Exit(1)
	}

	driveContext, err := google.NewGoogleDrive(store)
	if err != nil {
		//
		slog.Error("Failed to initialize the Google Drive service context", "error", err)
		os.Exit(1)
	}

	// lambdaApp := app.NewApp()
	lambda.Start(func(request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
		return NewFile(driveContext, request)

	})
}
