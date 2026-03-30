package util

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/mail"
	"path/filepath"
	"strings"

	"github.com/KyleBrandon/scriptor/pkg/types"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

func Assert[V comparable](got, expected V, message string) {
	if expected != got {
		panic(message)
	}
}

func BuildGatewayResponse(
	message string,
	statusCode int,
) (events.APIGatewayProxyResponse, error) {
	return events.APIGatewayProxyResponse{
		Body:       message,
		StatusCode: statusCode,
	}, nil
}

func BuildStepInput(notificationID, documentID, stage string) (string, error) {
	// Start the state machine with the document id and stage
	input := types.DocumentStep{
		NotificationID: notificationID,
		DocumentID:     documentID,
		Stage:          stage,
	}

	inputJSON, err := json.Marshal(input)
	if err != nil {
		slog.Error(
			"Failed to serialize the document information for the next step",
			"error",
			err,
		)
		return "", err
	}

	return string(inputJSON), nil
}

func GetNamePart(fullName string) string {

	ext := filepath.Ext(fullName)
	nameWithoutExt := strings.TrimSuffix(fullName, ext)

	return nameWithoutExt
}

func getSecret(
	ctx context.Context,
	sm *secretsmanager.Client,
	secretName string,
) (string, error) {

	input := &secretsmanager.GetSecretValueInput{SecretId: &secretName}

	result, err := sm.GetSecretValue(ctx, input)
	if err != nil {
		return "", err
	}

	return *result.SecretString, nil
}

// TODO: Make this into a generic
func GetDefaultFolderLocations(
	ctx context.Context,
	awsCfg aws.Config,
) (*types.GoogleFolderDefaultLocations, error) {

	sm := secretsmanager.NewFromConfig(awsCfg)

	// no watch channels yet, let's seed a default
	folderInfo, err := getSecret(
		ctx,
		sm,
		types.GOOGLE_FOLDER_DEFAULT_LOCATIONS_SECRETS,
	)
	if err != nil {
		slog.Error(
			"Failed to get the default folder locations from AWS secret manager",
			"error",
			err,
		)
		return nil, err
	}

	var folderLocations types.GoogleFolderDefaultLocations

	err = json.Unmarshal([]byte(folderInfo), &folderLocations)
	if err != nil {
		slog.Error(
			"Failed to unmarshal default Google folder locations from secret manager",
			"error",
			err,
		)
		return nil, err
	}

	return &folderLocations, nil
}

func CreateOpenAIClient(
	ctx context.Context,
	awsCfg aws.Config,
) (openai.Client, error) {

	svc := secretsmanager.NewFromConfig(awsCfg)

	secretName := types.OPENAI_SECRETS
	input := &secretsmanager.GetSecretValueInput{SecretId: &secretName}

	result, err := svc.GetSecretValue(ctx, input)
	if err != nil {
		return openai.Client{}, err
	}

	var openAISecrets types.OpenAISecrets

	err = json.Unmarshal([]byte(*result.SecretString), &openAISecrets)
	if err != nil {
		return openai.Client{}, err
	}

	client := openai.NewClient(option.WithAPIKey(openAISecrets.ApiKey))
	return client, nil
}

func LoadMathpixSecrets(
	ctx context.Context,
	awsCfg aws.Config,
) (*types.MathpixSecrets, error) {

	// New secrets manager from AWS
	svc := secretsmanager.NewFromConfig(awsCfg)

	secretName := types.MATHPIX_SECRETS
	input := &secretsmanager.GetSecretValueInput{SecretId: &secretName}

	result, err := svc.GetSecretValue(ctx, input)
	if err != nil {
		return nil, err
	}

	var mathpixSecrets types.MathpixSecrets

	err = json.Unmarshal([]byte(*result.SecretString), &mathpixSecrets)
	if err != nil {
		return nil, err
	}

	return &mathpixSecrets, nil

}

func LoadPostUploadEmailConfig(
	ctx context.Context,
	awsCfg aws.Config,
) (*types.PostUploadEmailConfig, error) {
	sm := secretsmanager.NewFromConfig(awsCfg)

	secret, err := getSecret(ctx, sm, types.POST_UPLOAD_EMAIL_SECRETS)
	if err != nil {
		slog.Error(
			"Failed to get the post-upload email config from AWS secret manager",
			"error",
			err,
		)
		return nil, err
	}

	var cfg types.PostUploadEmailConfig
	if err := json.Unmarshal([]byte(secret), &cfg); err != nil {
		slog.Error(
			"Failed to unmarshal the post-upload email config",
			"error",
			err,
		)
		return nil, err
	}

	cfg.SubjectPrefix = strings.TrimSpace(cfg.SubjectPrefix)
	if cfg.SubjectPrefix == "" {
		cfg.SubjectPrefix = "[Scriptor]"
	}

	if !cfg.Enabled {
		return &cfg, nil
	}

	cfg.FromAddress = strings.TrimSpace(cfg.FromAddress)
	if cfg.FromAddress == "" {
		return nil, fmt.Errorf("from_address is required when email sending is enabled")
	}

	if _, err := mail.ParseAddress(cfg.FromAddress); err != nil {
		return nil, fmt.Errorf("invalid from_address %q: %w", cfg.FromAddress, err)
	}

	cfg.ToAddresses, err = normalizeAndValidateAddresses(cfg.ToAddresses)
	if err != nil {
		return nil, err
	}
	if len(cfg.ToAddresses) == 0 {
		return nil, fmt.Errorf("to_addresses must contain at least one address when email sending is enabled")
	}

	cfg.ReplyTo, err = normalizeAndValidateAddresses(cfg.ReplyTo)
	if err != nil {
		return nil, err
	}

	return &cfg, nil
}

func normalizeAndValidateAddresses(addresses []string) ([]string, error) {
	normalized := []string{}

	for _, address := range addresses {
		address = strings.TrimSpace(address)
		if address == "" {
			continue
		}

		if _, err := mail.ParseAddress(address); err != nil {
			return nil, fmt.Errorf("invalid email address %q: %w", address, err)
		}

		normalized = append(normalized, address)
	}

	return normalized, nil
}
