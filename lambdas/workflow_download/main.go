package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/KyleBrandon/scriptor/lambdas/util"
	"github.com/KyleBrandon/scriptor/pkg/database"
	"github.com/KyleBrandon/scriptor/pkg/google"
	"github.com/KyleBrandon/scriptor/pkg/types"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sfn"
)

type downloadConfig struct {
	store           database.WatchChannelStore
	dc              *google.GoogleDriveContext
	awsCfg          aws.Config
	stateMachineARN string
	s3Client        *s3.Client
}

var (
	BucketName string = types.S3_BUCKET_NAME
	cfg        *downloadConfig
)

func (cfg *downloadConfig) verifyStoreConnection() error {
	if err := cfg.store.Ping(); err != nil {
		cfg.store, err = database.NewDynamoDBClient()
		if err != nil {
			slog.Error("Failed to configure the DynamoDB client", "error", err)
			return err
		}
	}

	return nil
}

func (cfg *downloadConfig) verifyDriveContext() error {
	if cfg.dc == nil {
		var err error
		cfg.dc, err = google.NewGoogleDrive(cfg.store)
		if err != nil {
			//
			slog.Error("Failed to initialize the Google Drive service context", "error", err)
			return err
		}
	}

	return nil
}

func (cfg *downloadConfig) copyDocument(document *types.Document) (string, error) {
	reader, err := cfg.dc.GetReader(document)
	if err != nil {
		slog.Error("Failed to get a reader for the document", "error", err)
		return "", err
	}

	defer reader.Close()

	// Upload to S3
	key := fmt.Sprintf("staging/%s-%d.pdf", document.Name, time.Now().Unix())
	_, err = cfg.s3Client.PutObject(context.TODO(), &s3.PutObjectInput{
		Bucket:        aws.String(BucketName),
		Key:           aws.String(key),
		Body:          reader,
		ContentType:   aws.String("application/pdf"),
		ContentLength: aws.Int64(document.Size),
	})
	if err != nil {
		slog.Error("Failed to save the document in the S3 bucket", "docName", document.Name, "error", err)
		return "", err
	}

	return key, nil
}

func getRequestFolderID(request events.APIGatewayProxyRequest) (string, error) {
	resourceState := request.Headers["X-Goog-Resource-State"]
	channelID := request.Headers["X-Goog-Channel-ID"]
	resourceID := request.Headers["X-Goog-Resource-ID"]

	// If we receive a 'sync' notification, ignore it for now.
	// We could use this for initialzing the state of the vault?
	if resourceState != "add" {
		slog.Debug("Webhook received non-add resource state", "channelID", channelID, "resourceState", resourceState)
		return "", fmt.Errorf("invalid file notification")
	}

	// query the watch channel based on the channelID
	wc, err := cfg.store.GetWatchChannelByChannel(channelID)
	if err != nil {
		slog.Error("Failed to find a registration for the channel", "channelID", channelID, "error", err)
		return "", fmt.Errorf("invalid file notification")

	}

	// verify the resourceID
	if resourceID != wc.ResourceID {
		slog.Error("ResourceID for the channel is not valid", "channelID", channelID, "resourceID", resourceID, "error", err)
		return "", fmt.Errorf("invalid file notification")
	}

	return wc.FolderID, nil
}

func (cfg *downloadConfig) processFileNotification(request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
	slog.Debug(">>processFileNotification")
	defer slog.Debug("<<processFileNotification")

	if err := cfg.verifyStoreConnection(); err != nil {
		return util.BuildGatewayResponse("Failed to initialize the connection to the database", http.StatusInternalServerError)
	}

	if err := cfg.verifyDriveContext(); err != nil {
		return util.BuildGatewayResponse("Failed to initialize the Google Drive API context", http.StatusInternalServerError)
	}

	folderID, err := getRequestFolderID(request)
	if err != nil {
		return util.BuildGatewayResponse(err.Error(), http.StatusInternalServerError)
	}

	// get the step function client
	sfnClient := sfn.NewFromConfig(cfg.awsCfg)

	// Check for new or modified files
	documents, err := cfg.dc.QueryFiles(folderID)
	if err != nil {
		slog.Error("Call to QueryFiles failed", "error", err)
		return util.BuildGatewayResponse("Failed to query for new files", http.StatusOK)
	}

	// loop through the documents that have been uploaded
	for _, document := range documents {

		// Save the Google Drive document information
		err = cfg.store.InsertDocument(*document)
		if err != nil {
			slog.Error("Failed to save the document metadata", "docName", document.Name, "error", err)
			continue
		}

		// Update the 'download' processing stage to in-progress
		stage := types.DocumentProcessingStage{
			ID:          document.ID,
			Stage:       types.DOCUMENT_STAGE_DOWNLOADED,
			StageStatus: types.DOCUMENT_STATUS_INPROGRESS,
		}

		err = cfg.store.InsertDocumentStage(stage)
		if err != nil {
			slog.Error("Failed to save the document processing stage", "error", err)
			continue
		}

		// copy the original document to S3
		path, err := cfg.copyDocument(document)
		if err != nil {
			continue
		}

		// Update the stage to complete
		stage.S3Key = path
		stage.StageStatus = types.DOCUMENT_STATUS_COMPLETE

		err = cfg.store.UpdateDocumentStage(stage)
		if err != nil {
			slog.Error("Failed to update the processing stage as complete", "error", err)
			continue
		}

		input, err := util.BuildStageInput(document.ID, types.DOCUMENT_STAGE_DOWNLOADED, document.Name)
		if err != nil {
			slog.Error("Failed to build the stage input for the next stage", "error", err)
			return util.BuildGatewayResponse("Failed to build the input for the state machine", http.StatusInternalServerError)
		}

		_, err = sfnClient.StartExecution(context.TODO(), &sfn.StartExecutionInput{
			StateMachineArn: &cfg.stateMachineARN,
			Input:           aws.String(input),
		})

		if err != nil {
			slog.Error("Failed to start the state machine execution", "error", err)
			return util.BuildGatewayResponse("Failed to start the state machine execution", http.StatusInternalServerError)
		}
	}

	return util.BuildGatewayResponse("Processing new file", http.StatusOK)
}

func init() {
	slog.Info(">>downloadLambda.init")
	defer slog.Info("<<downloadLambda.init")

	awsCfg, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		slog.Error("Failed to load the AWS config", "error", err)
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
		slog.Error("Failed to initialize the Google Drive service context", "error", err)
		os.Exit(1)
	}

	stateMachineARN := os.Getenv("STATE_MACHINE_ARN")
	if stateMachineARN == "" {
		slog.Error("Failed to get the state machine ARN", "error", err)
		os.Exit(1)
	}

	s3Client := s3.NewFromConfig(awsCfg)

	cfg = &downloadConfig{
		store,
		dc,
		awsCfg,
		stateMachineARN,
		s3Client,
	}

}

func main() {
	slog.Info(">>downloadLambda.main")
	defer slog.Info("<<downloadLambda.main")

	lambda.Start(func(request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
		return cfg.processFileNotification(request)
	})
}
