package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/KyleBrandon/scriptor/lambdas/util"
	"github.com/KyleBrandon/scriptor/pkg/database"
	"github.com/KyleBrandon/scriptor/pkg/google"
	"github.com/KyleBrandon/scriptor/pkg/types"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type handlerConfig struct {
	store    database.DocumentStore
	dc       *google.GoogleDriveContext
	s3Client *s3.Client
}

var (
	BucketName string = types.S3_BUCKET_NAME
	initOnce   sync.Once
	cfg        *handlerConfig
)

// Load all the inital configuration settings for the lambda
func loadConfiguration(ctx context.Context) (*handlerConfig, error) {

	cfg = &handlerConfig{}

	var err error

	awsCfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		slog.Error("failed to load the AWS config", "error", err)
		return nil, err
	}

	cfg.store, err = database.NewDocumentStore(ctx)
	if err != nil {
		slog.Error("Failed to configure the DynamoDB client", "error", err)
		return nil, err
	}

	cfg.dc, err = google.NewGoogleDrive(ctx)
	if err != nil {
		//
		slog.Error(
			"Failed to initialize the Google Drive service context",
			"error",
			err,
		)
		return nil, err
	}

	cfg.s3Client = s3.NewFromConfig(awsCfg)

	return cfg, nil
}

// Ensure that the configuration settings are only loaded once
func initLambda(ctx context.Context) error {
	var err error
	initOnce.Do(func() {
		slog.Debug(">>initLambda")
		defer slog.Debug("<<initLambda")

		cfg, err = loadConfiguration(ctx)
	})

	return err
}

// TODO: doesn't feel right updating the stage in here
func (cfg *handlerConfig) copyDocument(
	ctx context.Context,
	document *types.Document,
	stage *types.DocumentProcessingStage,
) error {
	// get a reader from Google Drive for the document
	reader, err := cfg.dc.GetReader(document)
	if err != nil {
		slog.Error("Failed to get a reader for the document", "error", err)
		return err
	}

	defer reader.Close()

	// get the name of the original document w/o extension
	documentName := util.GetNamePart(document.Name)

	// Save the original filename with the stage
	stage.OriginalFileName = document.Name

	// build the file name for the stage to have a timestamp
	stage.StageFileName = fmt.Sprintf(
		"%s-%d.pdf",
		documentName,
		time.Now().Unix(),
	)

	// construct the S3 Key for the file stage
	stage.S3Key = fmt.Sprintf("%s/%s", stage.Stage, stage.StageFileName)

	// store the file for the stage
	_, err = cfg.s3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(BucketName),
		Key:           aws.String(stage.S3Key),
		Body:          reader,
		ContentType:   aws.String("application/pdf"),
		ContentLength: aws.Int64(document.Size),
	})
	if err != nil {
		slog.Error(
			"Failed to save the document in the S3 bucket",
			"docName",
			document.Name,
			"error",
			err,
		)
		return err
	}

	return nil
}

func process(
	ctx context.Context,
	event types.DocumentStep,
) (types.DocumentStep, error) {
	slog.Debug(">>process")
	defer slog.Debug("<<process")

	ret := types.DocumentStep{}

	var err error

	if err := initLambda(ctx); err != nil {
		slog.Error("Failed to initialize the lambda", "error", err)
		return ret, err
	}

	// Query the document from Google Drive
	document, err := cfg.store.GetDocument(ctx, event.DocumentID)
	if err != nil {
		slog.Error(
			"Failed to query the document to download",
			"id",
			event.DocumentID,
			"error",
			err,
		)
		return ret, err
	}

	// create the download stage entry
	stage, err := cfg.store.StartDocumentStage(
		ctx,
		document.ID,
		types.DOCUMENT_STAGE_DOWNLOAD,
		document.Name,
	)
	if err != nil {
		slog.Error(
			"Failed to start the Mathpix document processing stage",
			"docName",
			document.Name,
			"error",
			err,
		)
		return ret, err
	}

	// copy the original document to S3
	err = cfg.copyDocument(ctx, document, stage)
	if err != nil {
		return ret, err
	}

	err = cfg.store.CompleteDocumentStage(ctx, stage)
	if err != nil {
		slog.Error(
			"Failed to update the processing stage as complete",
			"docName",
			document.Name,
			"error",
			err,
		)
		return ret, err
	}

	ret.NotificationID = event.NotificationID
	ret.DocumentID = document.ID
	ret.Stage = types.DOCUMENT_STAGE_DOWNLOAD

	return ret, nil
}

func main() {
	slog.Debug(">>main")
	defer slog.Debug("<<main")

	lambda.Start(process)
}
