package util

import (
	"encoding/json"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/KyleBrandon/scriptor/pkg/types"
	"github.com/aws/aws-lambda-go/events"
)

func BuildGatewayResponse(message string, statusCode int) (events.APIGatewayProxyResponse, error) {
	return events.APIGatewayProxyResponse{
		Body:       message,
		StatusCode: statusCode,
	}, nil
}

func BuildStageInput(id, stage, name string) (string, error) {
	// Start the state machine with the document id and stage
	input := types.DocumentStep{
		ID:           id,
		Stage:        stage,
		DocumentName: name,
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
