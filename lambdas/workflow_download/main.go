package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"

	"github.com/KyleBrandon/scriptor/lambdas/util"
	"github.com/KyleBrandon/scriptor/pkg/database"
	"github.com/KyleBrandon/scriptor/pkg/google"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

var (
	S3_BUCKET_NAME string = "S3_BUCKET_NAME"
)

type downloadConfig struct {
	store          database.WatchChannelStore
	dc             *google.GoogleDriveContext
	secretsManager *secretsmanager.Client
	s3Bucket       string
	awsCfg         aws.Config
}

func (cfg *downloadConfig) processFileNotification(dc *google.GoogleDriveContext, store database.WatchChannelStore, request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {

	slog.Info(">>processFileNotification")
	defer slog.Info("<<processFileNotification")

	// Extract headers sent by Google Drive
	resourceState := request.Headers["X-Goog-Resource-State"]
	channelID := request.Headers["X-Goog-Channel-ID"]
	resourceID := request.Headers["X-Goog-Resource-ID"]

	// If we receive a 'sync' notification, ignore it for now.
	// We could use this for initialzing the state of the vault?
	if resourceState != "add" {
		slog.Info("Webhook received non-add resource state", "channelID", channelID, "resourceState", resourceState)
		return util.BuildGatewayResponse("Only interested in new files that are 'add'ed", http.StatusOK, nil)
	}

	// TODO: query the watch channel based on the channelID and verify the resourceID
	wc, err := store.GetWatchChannelByChannel(channelID)
	if err != nil {
		message := "Failed to find a registration for the channel"

		slog.Error(message, "channelID", channelID, "error", err)
		return util.BuildGatewayResponse(message, http.StatusOK, nil)
	}

	// verify the resourceID
	if resourceID != wc.ResourceID {
		message := "ResourceID for the channel is not valid"
		slog.Error(message, "channelID", channelID, "resourceID", resourceID, "error", err)
		return util.BuildGatewayResponse(message, http.StatusOK, nil)
	}

	// Check for new or modified files
	documents, err := dc.QueryFiles(wc.FolderID)
	if err != nil {
		slog.Error("Call to QueryFiles failed", "error", err)
		return util.BuildGatewayResponse("Failed to query for new files", http.StatusOK, nil)
	}

	s3Client := s3.NewFromConfig(cfg.awsCfg)

	for _, document := range documents {
		slog.Info("process doc", "doc", document)

		reader, err := dc.GetReader(document)
		if err != nil {
			message := "Failed to get a reader for the document"
			slog.Error(message, "error", err)
			return util.BuildGatewayResponse(message, http.StatusOK, nil)
		}

		data, err := io.ReadAll(reader)
		if err != nil {
			message := "Failed to read the document data"
			slog.Error(message, "docName", document.Name, "error", err)
			return util.BuildGatewayResponse(message, http.StatusOK, nil)
		}

		// TODO: use the document.Name?
		key := fmt.Sprintf("staging/%s.pdf", document.ID)

		// Upload to S3
		_, err = s3Client.PutObject(context.TODO(), &s3.PutObjectInput{
			Bucket:      aws.String(cfg.s3Bucket),
			Key:         aws.String(key),
			Body:        bytes.NewReader(data),
			ContentType: aws.String("application/pdf"),
		})
		if err != nil {
			slog.Error("Failed to save the document in the S3 bucket", "docName", document.Name, "error", err)
			continue
		}

		// Set the document status
		document.Status = "downloaded"

		// TODO: Save metadata in DynamoDB
		err = store.InsertDocument(*document)
		if err != nil {
			slog.Error("Failed to save the document metadata", "docName", document.Name, "error", err)
			continue
		}

	}

	return util.BuildGatewayResponse("Processing new file", http.StatusOK, nil)
}

func main() {
	slog.Info(">>downloadLambda.main")
	defer slog.Info("<<downloadLambda.main")

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

	secretsManager := secretsmanager.NewFromConfig(awsCfg)

	input := &secretsmanager.GetSecretValueInput{SecretId: &S3_BUCKET_NAME}
	result, err := secretsManager.GetSecretValue(context.TODO(), input)
	if err != nil {
		// TODO
	}

	s3Bucket := *result.SecretString

	cfg := &downloadConfig{
		store,
		dc,
		secretsManager,

		s3Bucket,
		awsCfg}

	lambda.Start(func(request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
		slog.Info(">>downloadLambda.lambda")
		defer slog.Info("<<downloadLambda.lambda")

		return cfg.processFileNotification(dc, store, request)
	})
}
