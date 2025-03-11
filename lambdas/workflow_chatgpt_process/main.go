package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/KyleBrandon/scriptor/pkg/database"
	"github.com/KyleBrandon/scriptor/pkg/types"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

type chatgptConfig struct {
	store          database.WatchChannelStore
	secretsManager *secretsmanager.Client
}

var (
	BucketName string = types.S3_BUCKET_NAME
	cfg        *chatgptConfig
)

func (cfg *chatgptConfig) verifyStoreConnection() error {
	if err := cfg.store.Ping(); err != nil {
		cfg.store, err = database.NewDynamoDBClient()
		if err != nil {
			slog.Error("Failed to configure the DynamoDB client", "error", err)
			return err
		}
	}

	return nil
}

func (cfg *chatgptConfig) process(ctx context.Context, event types.DocumentStep) (types.DocumentStep, error) {
	slog.Info(">>chatgptLambda.process")
	defer slog.Info("<<chatgptLambda.process")

	slog.Info("chatgptLambda process input", "input", event)

	ret := types.DocumentStep{}

	if err := cfg.verifyStoreConnection(); err != nil {
		return ret, err
	}
	// Update the 'download' processing stage to in-progress
	stage := types.DocumentProcessingStage{
		ID:          event.ID,
		Stage:       types.DOCUMENT_STAGE_CHATGPT,
		StageStatus: types.DOCUMENT_STATUS_INPROGRESS,
	}

	err := cfg.store.InsertDocumentStage(stage)
	if err != nil {
		slog.Error("Failed to save the document processing stage", "error", err)
		return ret, err
	}

	// TODO: Send to ChatGPT for processing
	name := fmt.Sprintf("%s-%d.md", event.DocumentName, time.Now().Unix())
	key := fmt.Sprintf("chatgpt/%s", name)

	// Update the stage to complete
	stage.S3Key = key
	stage.StageStatus = types.DOCUMENT_STATUS_COMPLETE

	err = cfg.store.UpdateDocumentStage(stage)
	if err != nil {
		slog.Error("Failed to update the processing stage as complete", "error", err)
		return ret, err
	}

	// read doc from bucket
	slog.Info("Read file from S3 Bucket")
	ret.ID = event.ID
	ret.DocumentName = event.DocumentName
	ret.Stage = event.Stage

	slog.Info("chatgptLambda process output", "docs", ret)

	return ret, nil
}

func main() {
	slog.Info(">>chatgptLambda.main")
	defer slog.Info("<<chatgptLambda.main")

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

	secretsManager := secretsmanager.NewFromConfig(awsCfg)

	cfg := &chatgptConfig{
		store,
		secretsManager}

	lambda.Start(cfg.process)
}
