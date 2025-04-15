package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"

	"github.com/KyleBrandon/scriptor/lambdas/util"
	"github.com/KyleBrandon/scriptor/pkg/database"
	"github.com/KyleBrandon/scriptor/pkg/google"
	"github.com/KyleBrandon/scriptor/pkg/types"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sfn"
)

type handlerConfig struct {
	store           database.WatchChannelStore
	docStore        database.DocumentStore
	dc              *google.GoogleDriveContext
	stateMachineARN string
	sfnClient       *sfn.Client
}

var (
	initOnce sync.Once
	cfg      *handlerConfig
)

// Load all the inital configuration settings for the lambda
func loadConfiguration(ctx context.Context) (*handlerConfig, error) {

	cfg = &handlerConfig{}

	var err error
	cfg.store, err = database.NewWatchChannelStore(ctx)
	if err != nil {
		slog.Error("Failed to configure the DynamoDB client", "error", err)
		return nil, err
	}

	cfg.docStore, err = database.NewDocumentStore(ctx)
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

	awsCfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		slog.Error("Failed to load the AWS config", "error", err)
		return nil, err
	}

	cfg.stateMachineARN = os.Getenv("STATE_MACHINE_ARN")
	if cfg.stateMachineARN == "" {
		slog.Error("Failed to get the state machine ARN")
		return nil, err
	}

	// Create a Step Function Client to start the state machine later
	cfg.sfnClient = sfn.NewFromConfig(awsCfg)
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

func process(ctx context.Context, sqsEvent events.SQSEvent) error {
	slog.Debug(">>process")
	defer slog.Debug("<<process")

	if err := initLambda(ctx); err != nil {
		slog.Error("Failed to initialize the lambda", "error", err)
		return err
	}

	// loop through any SQS events
	for _, message := range sqsEvent.Records {
		// get the channel that triggered the event
		var eventData types.ChannelNotification
		if err := json.Unmarshal([]byte(message.Body), &eventData); err != nil {
			return fmt.Errorf("failed to unmarshal SQS message: %v", err)
		}

		// Acquire the changes lock on the channel
		startToken, err := cfg.store.AcquireChangesToken(
			ctx,
			eventData.ChannelID,
		)
		if err != nil {
			slog.Error(
				"Failed to acquire the watch channel changes lock",
				"error",
				err,
			)
			return err
		}

		// Query the files that have changed and get the next changes start token
		changes, err := cfg.dc.QueryChanges(eventData.FolderID, startToken)
		if err != nil {
			slog.Error("Call to QueryFiles failed", "error", err)
			return err
		}

		// Update the start token so we pick up any new changes next time
		err = cfg.store.ReleaseChangesToken(
			ctx,
			eventData.ChannelID,
			changes.NextStartToken,
		)
		if err != nil {
			slog.Error(
				"Failed to release the watch channel changes lock",
				"error",
				err,
			)
		}

		// Check if there are documents to process
		if len(changes.Documents) == 0 {
			return nil
		}

		slog.Info(
			"Found documents to process",
			"count",
			len(changes.Documents),
			"folderID",
			eventData.FolderID,
			"documents",
			changes.Documents,
		)

		// Start the state machine for each document discovered
		for _, document := range changes.Documents {
			slog.Info(
				"Processing document from queue",
				"name",
				document.Name,
				"notificationID",
				eventData.NotificationID,
			)

			// Check if we have already processed this document
			_, err = cfg.docStore.GetDocumentByGoogleID(ctx, document.GoogleID)
			if err == nil {
				// The document exists, ignore it
				slog.Warn(
					"Document already processed",
					"id",
					document.ID,
					"googleID",
					document.GoogleID,
					"name",
					document.Name,
				)
				continue
			}

			// Save the Google Drive document information
			err = cfg.docStore.InsertDocument(ctx, document)
			if err != nil {
				slog.Error(
					"Failed to save the document metadata",
					"docName",
					document.Name,
					"error",
					err,
				)
				return err
			}

			// TODO: this should be a different step type as it's the Google document ID not ours
			input, err := util.BuildStepInput(
				eventData.NotificationID,
				document.ID,
				types.DOCUMENT_STAGE_NEW,
			)
			if err != nil {
				slog.Error(
					"Failed to build the stage input for the next stage",
					"docName",
					document.Name,
					"error",
					err,
				)
				return err
			}

			// start the state machine
			_, err = cfg.sfnClient.StartExecution(ctx, &sfn.StartExecutionInput{
				StateMachineArn: &cfg.stateMachineARN,
				Input:           aws.String(input),
			})
			if err != nil {
				slog.Error(
					"Failed to start the stage machine for the document",
					"docName",
					document.Name,
					"error",
					err,
				)
				return err
			}
		}

	}
	return nil
}

func main() {
	slog.Debug(">>main")
	defer slog.Debug("<<main")

	lambda.Start(process)
}
