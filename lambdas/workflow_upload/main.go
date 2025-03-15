package main

import (
	"context"
	"io"
	"log/slog"
	"os"

	"github.com/KyleBrandon/scriptor/lambdas/util"
	"github.com/KyleBrandon/scriptor/pkg/database"
	"github.com/KyleBrandon/scriptor/pkg/google"
	"github.com/KyleBrandon/scriptor/pkg/types"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type uploadConfig struct {
	store    database.ScriptorStore
	dc       *google.GoogleDriveContext
	awsCfg   aws.Config
	s3Client *s3.Client
}

var (
	BucketName string = types.S3_BUCKET_NAME
	cfg        *uploadConfig
)

func (cfg *uploadConfig) getFileReaderForStage(s3FileKey string) (io.ReadCloser, error) {

	resp, err := cfg.s3Client.GetObject(context.TODO(), &s3.GetObjectInput{
		Bucket: aws.String(types.S3_BUCKET_NAME),
		Key:    aws.String(s3FileKey),
	})
	if err != nil {
		slog.Error("Failed to read the file processed by ChatGPT", "error", err)
		return nil, err
	}

	return resp.Body, nil

}

func (cfg *uploadConfig) saveStageToFolder(id, stage, folderID string) error {
	// Get the original document file
	docStage, err := cfg.store.GetDocumentStage(id, stage)
	if err != nil {
		slog.Error("Failed to read the document stage", "documentID", id, "stage", stage, "error", err)
		return err
	}

	// Get a reader from the S3 file location
	docReader, err := cfg.getFileReaderForStage(docStage.S3Key)
	if err != nil {
		slog.Error("Failed to get file reader for the ChatGPT processed document", "error", err)
		return err
	}

	defer docReader.Close()

	// Save the file to the destination folder
	err = cfg.dc.SaveFile(docStage.FileName, folderID, docReader)
	if err != nil {
		slog.Error("Failed to save the original document file to the destination folder", "error", err)
		return err
	}

	return nil
}

func (cfg *uploadConfig) process(ctx context.Context, event types.DocumentStep) error {
	slog.Info(">>uploadLambda.process")
	defer slog.Info("<<uploadLambda.process")

	slog.Info("uploadLambda process input", "input", event)

	var err error
	cfg.store, err = util.VerifyStoreConnection(cfg.store)
	if err != nil {
		slog.Error("Failed to verify the DynamoDB client", "error", err)
		return err
	}

	cfg.dc, err = util.VerifyDriveContext(cfg.dc, cfg.store)
	if err != nil {
		return err
	}

	// Start the document upload stage
	uploadStage, err := cfg.store.StartDocumentStage(event.ID, types.DOCUMENT_STAGE_UPLOAD, types.DOCUMENT_STATUS_INPROGRESS)
	if err != nil {
		slog.Error("Failed to start the Mathpix document processing stage", "error", err)
		return err
	}

	// Get the folder locations from secret manager
	folderLocations, err := util.GetDefaultFolderLocations()
	if err != nil {
		slog.Error("Failed to read the default folder locations for Google Drive", "error", err)
		return err
	}

	// Save the original PDF file to the destination folder
	err = cfg.saveStageToFolder(event.ID, types.DOCUMENT_STAGE_DOWNLOADED, folderLocations.DestFolderID)
	if err != nil {
		slog.Error("Failed to save the original PDF to the destination folder", "id", event.ID, "folderID", folderLocations.FolderID, "error", err)
		return err
	}

	// Save the output from the last stage to the destination folder
	err = cfg.saveStageToFolder(event.ID, event.Stage, folderLocations.DestFolderID)
	if err != nil {
		slog.Error("Failed to save the output stage to the destination folder", "id", event.ID, "stage", event.Stage, "folderID", folderLocations.FolderID, "error", err)
		return err
	}

	// TODO: Archive the original PDF in the watch folder
	document, err := cfg.store.GetDocument(event.ID)
	if err != nil {
		slog.Error("Failed to get the document information to archive", "id", event.ID, "error", err)
		return err
	}

	err = cfg.dc.Archive(&document, folderLocations.ArchiveFolderID)
	if err != nil {
		slog.Error("Failed to archive the document", "id", event.ID, "error", err)
		return err
	}

	// Update the stage to complete
	uploadStage.StageStatus = types.DOCUMENT_STATUS_COMPLETE
	err = cfg.store.UpdateDocumentStage(uploadStage)
	if err != nil {
		slog.Error("Failed to update the processing stage as complete", "error", err)
		return err
	}

	return nil
}

func init() {
	slog.Info(">>uploadLambda.init")
	defer slog.Info("<<uploadLambda.init")

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

	s3Client := s3.NewFromConfig(awsCfg)

	cfg = &uploadConfig{
		store,
		dc,
		awsCfg,
		s3Client,
	}

}

func main() {
	slog.Info(">>uploadLambda.main")
	defer slog.Info("<<uploadLambda.main")

	lambda.Start(cfg.process)
}
