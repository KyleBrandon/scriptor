package util

import (
	"github.com/aws/aws-lambda-go/events"
)

func BuildGatewayResponse(message string, statusCode int) (events.APIGatewayProxyResponse, error) {
	return events.APIGatewayProxyResponse{
		Body:       message,
		StatusCode: statusCode,
	}, nil
}
