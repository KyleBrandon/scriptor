package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/KyleBrandon/scriptor/lambdas/util"
	"github.com/KyleBrandon/scriptor/pkg/database"
	"github.com/KyleBrandon/scriptor/pkg/types"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

type handlerConfig struct {
	store     database.WatchChannelStore
	awsCfg    aws.Config
	sqsClient *sqs.Client
	queueURL  string
}

var cfg *handlerConfig

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
	wc, err := cfg.store.GetWatchChannelByID(channelID)
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

func (cfg *handlerConfig) processFileNotification(request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
	slog.Debug(">>processFileNotification")
	defer slog.Debug("<<processFileNotification")

	var err error

	// Parse the folderID from the gateway request
	wc, err := queryWatchChannelForRequest(request)
	if err != nil {
		return util.BuildGatewayResponse(err.Error(), http.StatusInternalServerError)
	}

	message := types.ChannelNotification{
		ChannelID: wc.ChannelID,
		FolderID:  wc.FolderID,
	}

	messageBody, err := json.Marshal(&message)
	if err != nil {
		return events.APIGatewayProxyResponse{StatusCode: 500}, err
	}

	_, err = cfg.sqsClient.SendMessage(context.TODO(), &sqs.SendMessageInput{
		QueueUrl:    &cfg.queueURL,
		MessageBody: aws.String(string(messageBody)),
	})
	if err != nil {
		return events.APIGatewayProxyResponse{StatusCode: 500}, err
	}

	return util.BuildGatewayResponse("Processing new file", http.StatusOK)
}

func init() {
	slog.Debug(">>init")
	defer slog.Debug("<<init")

	cfg = &handlerConfig{}

	var err error
	cfg.awsCfg, err = config.LoadDefaultConfig(context.TODO())
	if err != nil {
		slog.Error("Failed to load the AWS config", "error", err)
		os.Exit(1)
	}

	// Create an SQS client
	cfg.sqsClient = sqs.NewFromConfig(cfg.awsCfg)

	// load the SQS URL that was configured
	cfg.queueURL = os.Getenv("SQS_QUEUE_URL")
	if cfg.queueURL == "" {
		slog.Error("SQS URL is not configured")
		os.Exit(1)
	}

	// Create a storage client if we don't have one
	cfg.store, err = util.VerifyWatchChannelStore(cfg.store)
	if err != nil {
		os.Exit(1)
	}
}

func main() {
	slog.Debug(">>main")
	defer slog.Debug("<<main")

	lambda.Start(func(request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
		return cfg.processFileNotification(request)
	})
}
