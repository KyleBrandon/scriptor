package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/KyleBrandon/scriptor/pkg/database"
	"github.com/KyleBrandon/scriptor/pkg/google"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

type mathpixConfig struct {
	store          database.WatchChannelStore
	dc             *google.GoogleDriveContext
	secretsManager *secretsmanager.Client
}

func (cfg *mathpixConfig) process() error {
	slog.Info(">>mathpixLambda.process")
	defer slog.Info("<<mathpixLambda.process")

	return nil
}

func main() {
	slog.Info(">>mathpixLambda.main")
	defer slog.Info("<<mathpixLambda.main")

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

	cfg := &mathpixConfig{
		store,
		dc,
		secretsManager}

	lambda.Start(cfg.process)
}
