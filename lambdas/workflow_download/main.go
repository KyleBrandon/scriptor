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
	store           database.ScriptorStore
	dc              *google.GoogleDriveContext
	awsCfg          aws.Config
	stateMachineARN string
	s3Client        *s3.Client
}

var (
	BucketName string = types.S3_BUCKET_NAME
	cfg        *downloadConfig
)

func (cfg *downloadConfig) copyDocument(document *types.Document, stage *types.DocumentProcessingStage) error {
	reader, err := cfg.dc.GetReader(document)
	if err != nil {
		slog.Error("Failed to get a reader for the document", "error", err)
		return err
	}

	defer reader.Close()

	// get the name of the original document w/o extension
	documentName := util.GetDocumentName(document.Name)

	// Save the original filename with the stage
	stage.OriginalFileName = document.Name

	// build the file name for the stage to have a timestamp
	stage.StageFileName = fmt.Sprintf("%s-%d.pdf", documentName, time.Now().Unix())

	// construct the S3 Key for the file stage
	stage.S3Key = fmt.Sprintf("%s/%s", stage.Stage, stage.StageFileName)

	// store the file for the stage
	_, err = cfg.s3Client.PutObject(context.TODO(), &s3.PutObjectInput{
		Bucket:        aws.String(BucketName),
		Key:           aws.String(stage.S3Key),
		Body:          reader,
		ContentType:   aws.String("application/pdf"),
		ContentLength: aws.Int64(document.Size),
	})
	if err != nil {
		slog.Error("Failed to save the document in the S3 bucket", "docName", document.Name, "error", err)
		return err
	}

	return nil
}

func queryWatchChannelForRequest(request events.APIGatewayProxyRequest) (*types.WatchChannel, error) {
	resourceState := request.Headers["X-Goog-Resource-State"]
	channelID := request.Headers["X-Goog-Channel-ID"]
	resourceID := request.Headers["X-Goog-Resource-ID"]

	// If we receive a 'sync' notification, ignore it for now.
	// We could use this for initialzing the state of the vault?
	if resourceState != "add" {
		slog.Debug("Webhook received non-add resource state", "channelID", channelID, "resourceState", resourceState)
		return nil, fmt.Errorf("invalid file notification")
	}

	// query the watch channel based on the channelID
	wc, err := cfg.store.GetWatchChannelByChannel(channelID)
	if err != nil {
		slog.Error("Failed to find a registration for the channel", "channelID", channelID, "error", err)
		return nil, fmt.Errorf("invalid file notification")

	}

	// verify the resourceID
	if resourceID != wc.ResourceID {
		slog.Error("ResourceID for the channel is not valid", "channelID", channelID, "resourceID", resourceID, "error", err)
		return nil, fmt.Errorf("invalid file notification")
	}

	return &wc, nil
}

func (cfg *downloadConfig) processDocuments(documents []*types.Document) error {
	// Create a Step Function Client to start the state machine later
	sfnClient := sfn.NewFromConfig(cfg.awsCfg)

	// loop through the documents that have been uploaded
	for _, document := range documents {
		slog.Info("process document", "docName", document.Name)

		// Save the Google Drive document information
		err := cfg.store.InsertDocument(*document)
		if err != nil {
			slog.Error("Failed to save the document metadata", "docName", document.Name, "error", err)
			return err
		}

		// Start document stage to in-progress
		stage, err := cfg.store.StartDocumentStage(document.ID, types.DOCUMENT_STAGE_DOWNLOADED, document.Name)
		if err != nil {
			slog.Error("Failed to save the document processing stage", "docName", document.Name, "error", err)
			return err
		}

		// copy the original document to S3
		err = cfg.copyDocument(document, &stage)
		if err != nil {
			return err
		}

		// update the document stage to complete
		err = cfg.store.CompleteDocumentStage(stage)
		if err != nil {
			slog.Error("Failed to update the processing stage as complete", "docName", document.Name, "error", err)
			return err
		}

		input, err := util.BuildStageInput(document.ID, types.DOCUMENT_STAGE_DOWNLOADED)
		if err != nil {
			slog.Error("Failed to build the stage input for the next stage", "docName", document.Name, "error", err)
			return err
		}

		slog.Info("downloadLambda stage output", "event", input)

		// start the state machine execution
		_, err = sfnClient.StartExecution(context.TODO(), &sfn.StartExecutionInput{
			StateMachineArn: &cfg.stateMachineARN,
			Input:           aws.String(input),
		})

		if err != nil {
			slog.Error("Failed to start the state machine execution", "docName", document.Name, "error", err)
			return err
		}
	}

	return nil
}

func (cfg *downloadConfig) processFileNotification(request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
	slog.Debug(">>processFileNotification")
	defer slog.Debug("<<processFileNotification")

	var err error

	// Create a storage client if we don't have one
	cfg.store, err = util.VerifyStoreConnection(cfg.store)
	if err != nil {
		slog.Error("Failed to verify the DynamoDB client", "error", err)
		return util.BuildGatewayResponse("Failed to initialize the connection to the database", http.StatusInternalServerError)
	}

	// Create a Google Drive service if we don't have one
	cfg.dc, err = util.VerifyDriveContext(cfg.dc, cfg.store)
	if err != nil {
		return util.BuildGatewayResponse("Failed to initialize the Google Drive API context", http.StatusInternalServerError)
	}

	// Parse the folderID from the gateway request
	wc, err := queryWatchChannelForRequest(request)
	if err != nil {
		return util.BuildGatewayResponse(err.Error(), http.StatusInternalServerError)
	}

	// Check for new or modified files
	documents, err := cfg.dc.QueryFiles(wc.FolderID)
	if err != nil {
		slog.Error("Call to QueryFiles failed", "error", err)
		return util.BuildGatewayResponse("Failed to query for new files", http.StatusInternalServerError)
	}

	// Check if there are documents to process
	if len(documents) == 0 {
		return util.BuildGatewayResponse("No documents to process", http.StatusOK)
	}

	// process any documents that were moved to the watch folder
	err = cfg.processDocuments(documents)
	if err != nil {
		slog.Error("Failed to process the documents in Google Drive", "error", err)
		return util.BuildGatewayResponse("Failed to process the documents", http.StatusInternalServerError)
	}

	return util.BuildGatewayResponse("Processing new file", http.StatusOK)
}

func init() {
	slog.Debug(">>init")
	defer slog.Debug("<<init")

	var err error
	cfg = &downloadConfig{}

	cfg.awsCfg, err = config.LoadDefaultConfig(context.TODO())
	if err != nil {
		slog.Error("Failed to load the AWS config", "error", err)
		os.Exit(1)
	}

	cfg.stateMachineARN = os.Getenv("STATE_MACHINE_ARN")
	if cfg.stateMachineARN == "" {
		slog.Error("Failed to get the state machine ARN", "error", err)
		os.Exit(1)
	}

	cfg.s3Client = s3.NewFromConfig(cfg.awsCfg)

}

func main() {
	slog.Debug(">>main")
	defer slog.Debug("<<main")

	lambda.Start(func(request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
		return cfg.processFileNotification(request)
	})
}
