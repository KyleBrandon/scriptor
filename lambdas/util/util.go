package util

import (
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/KyleBrandon/scriptor/pkg/database"
	"github.com/KyleBrandon/scriptor/pkg/google"
	"github.com/KyleBrandon/scriptor/pkg/types"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

func BuildGatewayResponse(message string, statusCode int) (events.APIGatewayProxyResponse, error) {
	return events.APIGatewayProxyResponse{
		Body:       message,
		StatusCode: statusCode,
	}, nil
}

func BuildStageInput(id, stage string) (string, error) {
	// Start the state machine with the document id and stage
	input := types.DocumentStep{
		ID:    id,
		Stage: stage,
	}

	inputJSON, err := json.Marshal(input)
	if err != nil {
		slog.Error("Failed to serialize the document information for the next step", "error", err)
		return "", err
	}

	return string(inputJSON), nil
}

func GetDocumentName(fullName string) string {

	ext := filepath.Ext(fullName)
	nameWithoutExt := strings.TrimSuffix(fullName, ext)

	return nameWithoutExt
}

func getSecret(sm *secretsmanager.Client, secretName string) (string, error) {

	input := &secretsmanager.GetSecretValueInput{SecretId: &secretName}

	result, err := sm.GetSecretValue(context.TODO(), input)
	if err != nil {
		return "", err
	}

	return *result.SecretString, nil
}

// TODO: Make this into a generic
func GetDefaultFolderLocations() (*types.GoogleFolderDefaultLocations, error) {

	awsCfg, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		slog.Error("failed to load the AWS config", "error", err)
		return nil, err
	}

	sm := secretsmanager.NewFromConfig(awsCfg)

	// no watch channels yet, let's seed a default
	folderInfo, err := getSecret(sm, types.GOOGLE_FOLDER_DEFAULT_LOCATIONS_SECRETS)
	if err != nil {
		slog.Error("Failed to get the default folder locations from AWS secret manager", "error", err)
		return nil, err
	}

	var folderLocations types.GoogleFolderDefaultLocations

	err = json.Unmarshal([]byte(folderInfo), &folderLocations)
	if err != nil {
		slog.Error("Failed to unmarshal default Google folder locations from secret manager", "error", err)
		return nil, err
	}

	return &folderLocations, nil
}

// Verify the DynamoDB storage connection and create a new one if it has been closed for any reason.
func VerifyStoreConnection(store database.ScriptorStore) (database.ScriptorStore, error) {
	var err error

	// if we do not have a store initialized, then create one
	if store == nil {
		store, err = database.NewDynamoDBClient()
		if err != nil {
			slog.Error("Failed to configure the DynamoDB client", "error", err)
			return nil, err
		}

		return store, nil
	}

	// make sure we can query the database
	if err = store.Ping(); err != nil {
		// create
		store, err = database.NewDynamoDBClient()
		if err != nil {
			slog.Error("Failed to configure the DynamoDB client", "error", err)
			return nil, err
		}
	}

	return store, nil
}

// Verify the Google Drive service connection and create a new one if it is not valid.
func VerifyDriveContext(driveContext *google.GoogleDriveContext, store database.ScriptorStore) (*google.GoogleDriveContext, error) {
	if driveContext == nil {
		var err error
		driveContext, err = google.NewGoogleDrive(store)
		if err != nil {
			//
			slog.Error("Failed to initialize the Google Drive service context", "error", err)
			return nil, err
		}
	}

	return driveContext, nil
}
