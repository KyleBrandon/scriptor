package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"sync"

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
	store           database.DocumentStore
	dc              *google.GoogleDriveContext
	folderLocations *types.GoogleFolderDefaultLocations
	s3Client        *s3.Client
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

	// Get the folder locations from secret manager
	cfg.folderLocations, err = util.GetDefaultFolderLocations(ctx, awsCfg)
	if err != nil {
		slog.Error(
			"Failed to read the default folder locations for Google Drive",
			"error",
			err,
		)
		return nil, err
	}

	cfg.s3Client = s3.NewFromConfig(awsCfg)

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

func (cfg *handlerConfig) getFileReaderForStage(
	ctx context.Context,
	s3FileKey string,
) (io.ReadCloser, error) {

	resp, err := cfg.s3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(types.S3_BUCKET_NAME),
		Key:    aws.String(s3FileKey),
	})
	if err != nil {
		slog.Error("Failed to read the file processed by ChatGPT", "error", err)
		return nil, err
	}

	return resp.Body, nil

}

func (cfg *handlerConfig) saveStageToFolder(
	ctx context.Context,
	docStage *types.DocumentProcessingStage,
	folderID, baseName string,
) error {

	// Get a reader from the S3 file location
	docReader, err := cfg.getFileReaderForStage(ctx, docStage.S3Key)
	if err != nil {
		slog.Error(
			"Failed to get file reader for the ChatGPT processed document",
			"error",
			err,
		)
		return err
	}

	defer docReader.Close()

	// Stages append a timestamp to file names for processing and we want to
	// save the file with the original file name and the extension from the stage
	fileName := fmt.Sprintf(
		"%s%s",
		baseName,
		filepath.Ext(docStage.StageFileName),
	)

	// Save the file to the destination folder
	err = cfg.dc.SaveFile(fileName, folderID, docReader)
	if err != nil {
		slog.Error(
			"Failed to save the original document file to the destination folder",
			"error",
			err,
		)
		return err
	}

	return nil
}

func process(ctx context.Context, event types.DocumentStep) error {
	slog.Debug(">>process")
	defer slog.Debug("<<process")

	if err := initLambda(ctx); err != nil {
		slog.Error("Failed to initialize the lambda", "error", err)
		return err
	}

	// query the previous stage information
	prevStage, err := cfg.store.GetDocumentStage(
		ctx,
		event.DocumentID,
		event.Stage,
	)
	if err != nil {
		slog.Error(
			"Failed to get the previous stage information",
			"id",
			event.DocumentID,
			"stage",
			event.Stage,
			"error",
			err,
		)
		return err
	}

	// Start the document upload stage
	uploadStage, err := cfg.store.StartDocumentStage(
		ctx,
		event.DocumentID,
		types.DOCUMENT_STAGE_UPLOAD,
		prevStage.OriginalFileName,
	)
	if err != nil {
		slog.Error(
			"Failed to start the Mathpix document processing stage",
			"error",
			err,
		)
		return err
	}

	// query the download stage information stage information to get the original file
	downloadedStage, err := cfg.store.GetDocumentStage(
		ctx,
		event.DocumentID,
		types.DOCUMENT_STAGE_DOWNLOAD,
	)
	if err != nil {
		slog.Error(
			"Failed to get the Document Downloaded Stage information",
			"id",
			event.DocumentID,
			"error",
			err,
		)
		return err
	}

	// get the document record from DynamoDB
	document, err := cfg.store.GetDocument(ctx, event.DocumentID)
	if err != nil {
		slog.Error(
			"Failed to get the document information to archive",
			"id",
			event.DocumentID,
			"error",
			err,
		)
		return err
	}

	baseName := util.GetNamePart(document.Name)

	// Save the original PDF file to the destination folder
	err = cfg.saveStageToFolder(
		ctx,
		downloadedStage,
		cfg.folderLocations.DestFolderID,
		baseName,
	)
	if err != nil {
		slog.Error(
			"Failed to save the original PDF to the destination folder",
			"id",
			event.DocumentID,
			"folderID",
			cfg.folderLocations.FolderID,
			"error",
			err,
		)
		return err
	}

	// Save the output from the last stage to the destination folder
	err = cfg.saveStageToFolder(
		ctx,
		prevStage,
		cfg.folderLocations.DestFolderID,
		baseName,
	)
	if err != nil {
		slog.Error(
			"Failed to save the final output stage to the destination folder",
			"id",
			event.DocumentID,
			"stage",
			prevStage.Stage,
			"folderID",
			cfg.folderLocations.FolderID,
			"error",
			err,
		)
		return err
	}

	err = cfg.dc.Archive(document.GoogleID, cfg.folderLocations.ArchiveFolderID)
	if err != nil {
		slog.Error(
			"Failed to archive the document",
			"id",
			event.DocumentID,
			"folderID",
			cfg.folderLocations.ArchiveFolderID,
			"error",
			err,
		)
		return err
	}

	// Update the stage to complete
	err = cfg.store.CompleteDocumentStage(ctx, uploadStage)
	if err != nil {
		slog.Error(
			"Failed to update the processing stage as complete",
			"error",
			err,
		)
		return err
	}

	return nil
}

func main() {
	slog.Debug(">>main")
	defer slog.Debug("<<main")

	lambda.Start(process)
}
