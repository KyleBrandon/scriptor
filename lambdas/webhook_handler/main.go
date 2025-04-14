package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"

	"github.com/KyleBrandon/scriptor/lambdas/util"
	"github.com/KyleBrandon/scriptor/pkg/database"
	"github.com/KyleBrandon/scriptor/pkg/types"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/google/uuid"
)

type handlerConfig struct {
	store     database.WatchChannelStore
	sqsClient *sqs.Client
	queueURL  string
}

var (
	initOnce sync.Once
	cfg      *handlerConfig
)

// Load all the inital configuration settings for the lambda
func loadConfiguration(ctx context.Context) (*handlerConfig, error) {

	cfg = &handlerConfig{}

	var err error

	// load the SQS URL that was configured
	cfg.queueURL = os.Getenv("SQS_QUEUE_URL")
	if cfg.queueURL == "" {
		slog.Error("SQS URL is not configured")
		return nil, fmt.Errorf("failed to load the SQS URL fron the environment")
	}

	cfg.store, err = database.NewWatchChannelStore(ctx)
	if err != nil {
		slog.Error("Failed to configure the DynamoDB client", "error", err)
		return nil, err
	}

	// Load the default AWS config
	awsCfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		slog.Error("Failed to load the AWS config", "error", err)
		return nil, err
	}

	// Create an SQS client
	cfg.sqsClient = sqs.NewFromConfig(awsCfg)

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

func queryWatchChannelForRequest(ctx context.Context, request events.APIGatewayProxyRequest) (*types.WatchChannel, error) {
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
	wc, err := cfg.store.GetWatchChannelByID(ctx, channelID)
	if err != nil {
		slog.Error("Failed to find a registration for the channel", "channelID", channelID, "error", err)
		return nil, fmt.Errorf("invalid file notification")

	}

	// verify the resourceID
	if resourceID != wc.ResourceID {
		slog.Error("ResourceID for the channel is not valid", "channelID", channelID, "resourceID", resourceID, "error", err)
		return nil, fmt.Errorf("invalid file notification")
	}

	return wc, nil
}

func process(ctx context.Context, request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
	slog.Debug(">>processFileNotification")
	defer slog.Debug("<<processFileNotification")

	if err := initLambda(ctx); err != nil {
		slog.Error("Failed to initialize the lambda", "error", err)
		return util.BuildGatewayResponse(err.Error(), http.StatusInternalServerError)
	}

	// Parse the folderID from the gateway request
	wc, err := queryWatchChannelForRequest(ctx, request)
	if err != nil {
		return util.BuildGatewayResponse(err.Error(), http.StatusInternalServerError)
	}

	message := types.ChannelNotification{
		NotificationID: uuid.New().String(),
		ChannelID:      wc.ChannelID,
		FolderID:       wc.FolderID,
	}

	messageBody, err := json.Marshal(&message)
	if err != nil {
		return util.BuildGatewayResponse(err.Error(), http.StatusInternalServerError)
	}

	slog.Info("Sending SQS message", "channeID", wc.ChannelID, "folderID", wc.FolderID)

	_, err = cfg.sqsClient.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:    &cfg.queueURL,
		MessageBody: aws.String(string(messageBody)),
	})
	if err != nil {
		return util.BuildGatewayResponse(err.Error(), http.StatusInternalServerError)
	}

	return util.BuildGatewayResponse("Processing new file", http.StatusOK)
}

func main() {
	slog.Debug(">>main")
	defer slog.Debug("<<main")

	lambda.Start(process)
}
