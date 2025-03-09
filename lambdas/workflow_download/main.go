package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/sfn"
)

type downloadConfig struct {
	store           database.WatchChannelStore
	dc              *google.GoogleDriveContext
	secretsManager  *secretsmanager.Client
	awsCfg          aws.Config
	sfnClient       *sfn.Client
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

	buf := new(bytes.Buffer)
	size, err := io.Copy(buf, reader)
	if err != nil {
		slog.Error("Faield to copy the file from Google Drive", "error", err)
		return "", err
	}

	// Upload to S3
	key := fmt.Sprintf("staging/%s-%d.pdf", document.Name, time.Now().Unix())
	_, err = cfg.s3Client.PutObject(context.TODO(), &s3.PutObjectInput{
		Bucket:        aws.String(BucketName),
		Key:           aws.String(key),
		Body:          bytes.NewReader(buf.Bytes()),
		ContentType:   aws.String("application/pdf"),
		ContentLength: aws.Int64(size),
	})
	if err != nil {
		slog.Error("Failed to save the document in the S3 bucket", "docName", document.Name, "error", err)
		return "", err
	}

	return fmt.Sprintf("s3://%s/%s", BucketName, key), nil
}

func getRequestFolderID(request events.APIGatewayProxyRequest) (string, error) {
	resourceState := request.Headers["X-Goog-Resource-State"]
	channelID := request.Headers["X-Goog-Channel-ID"]
	resourceID := request.Headers["X-Goog-Resource-ID"]

	// If we receive a 'sync' notification, ignore it for now.
	// We could use this for initialzing the state of the vault?
	if resourceState != "add" {
		slog.Info("Webhook received non-add resource state", "channelID", channelID, "resourceState", resourceState)
		return "", fmt.Errorf("invalid file notification")
	}

	// TODO: query the watch channel based on the channelID and verify the resourceID
	wc, err := cfg.store.GetWatchChannelByChannel(channelID)
	if err != nil {
		message := "Failed to find a registration for the channel"
		slog.Error(message, "channelID", channelID, "error", err)
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
	slog.Info(">>processFileNotification")
	defer slog.Info("<<processFileNotification")

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

	// Check for new or modified files
	documents, err := cfg.dc.QueryFiles(folderID)
	if err != nil {
		slog.Error("Call to QueryFiles failed", "error", err)
		return util.BuildGatewayResponse("Failed to query for new files", http.StatusOK)
	}

	for _, document := range documents {

		path, err := cfg.copyDocument(document)
		if err != nil {
			continue
		}

		// Save metadata in DynamoDB
		document.Status = "downloaded"
		err = cfg.store.InsertDocument(*document)
		if err != nil {
			slog.Error("Failed to save the document metadata", "docName", document.Name, "error", err)
			continue
		}

		d := types.DocumentDownload{
			DocumentID:   document.ID,
			DocumentPath: path,
		}

		input := types.DocumentProcessInput{
			Document: d,
		}

		inputJSON, err := json.Marshal(input)
		if err != nil {
			slog.Error("Failed to serialize the document information for the next step", "error", err)
			continue
		}

		execOutput, err := cfg.sfnClient.StartExecution(context.TODO(), &sfn.StartExecutionInput{
			StateMachineArn: &cfg.stateMachineARN,
			Input:           aws.String(string(inputJSON)),
		})
		if err != nil {
			slog.Error("Failed to start the state machine execution", "error", err)
			return util.BuildGatewayResponse("Failed to start the state machine execution", http.StatusInternalServerError)
		}

		slog.Info("Step Function start successfully", "execARN", *execOutput.ExecutionArn)
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

	secretsManager := secretsmanager.NewFromConfig(awsCfg)

	sfnClient := sfn.NewFromConfig(awsCfg)

	stateMachineARN := os.Getenv("STATE_MACHINE_ARN")
	if stateMachineARN == "" {
		slog.Error("Failed to get the state machine ARN", "error", err)
		os.Exit(1)
	}

	s3Client := s3.NewFromConfig(awsCfg)

	cfg = &downloadConfig{
		store,
		dc,
		secretsManager,
		awsCfg,
		sfnClient,
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
