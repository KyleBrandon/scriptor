package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/KyleBrandon/scriptor/pkg/database"
	"github.com/KyleBrandon/scriptor/pkg/types"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

type mathpixConfig struct {
	store          database.WatchChannelStore
	secretsManager *secretsmanager.Client
	awsCfg         aws.Config
}

var (
	BucketName string = types.S3_BUCKET_NAME
	cfg        *mathpixConfig
)

func (cfg *mathpixConfig) verifyStoreConnection() error {
	if err := cfg.store.Ping(); err != nil {
		cfg.store, err = database.NewDynamoDBClient()
		if err != nil {
			slog.Error("Failed to configure the DynamoDB client", "error", err)
			return err
		}
	}

	return nil
}

func (cfg *mathpixConfig) process(ctx context.Context, event types.DocumentProcessInput) (types.DocumentProcessOutput, error) {
	slog.Info(">>mathpixLambda.process")
	defer slog.Info("<<mathpixLambda.process")

	slog.Info("mathpixLambda process input", "input", event)

	ret := types.DocumentProcessOutput{}

	if err := cfg.verifyStoreConnection(); err != nil {
		return ret, err
	}

	// read doc from bucket
	slog.Info("Read file from S3 Bucket")
	ret.DocumentProcessInput = event

	for _, d := range ret.Documents {
		d.MathpixDocumentPath = "abc"
	}

	slog.Info("mathpixLambda process output", "docs", ret)

	return ret, nil
}

func init() {
	slog.Debug(">>mathpixLambda.init")
	defer slog.Debug("<<mathpixLambda.init")
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

	secretsManager := secretsmanager.NewFromConfig(awsCfg)

	cfg = &mathpixConfig{
		store,
		secretsManager,

		awsCfg,
	}
}

func main() {
	slog.Info(">>mathpixLambda.main")
	defer slog.Info("<<mathpixLambda.main")

	lambda.Start(cfg.process)
}
