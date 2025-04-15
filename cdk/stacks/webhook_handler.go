package stacks

import (
	"fmt"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsapigateway"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslambda"
	"github.com/aws/jsii-runtime-go"
)

func (cfg *CdkScriptorConfig) NewWebhookHandlerStack(id string) awscdk.Stack {
	stack := awscdk.NewStack(cfg.App, &id, &cfg.Props.StackProps)

	// Define Lambda functions for workflow steps
	webhookLambda := awslambda.NewFunction(
		stack,
		jsii.String("scriptorWebhookHandlerLambda"),
		&awslambda.FunctionProps{
			Runtime: awslambda.Runtime_PROVIDED_AL2(),
			Code: awslambda.AssetCode_FromAsset(
				jsii.String("../bin/webhook_handler.zip"),
				nil,
			), // Path to compiled Go binary
			Handler: jsii.String("main"),
			Timeout: awscdk.Duration_Minutes(jsii.Number(5)),
			Environment: &map[string]*string{
				"SQS_QUEUE_URL": jsii.String(*cfg.documentQueue.QueueUrl()),
			},
		},
	)

	// grant the lambda read permissions to the watch channel table
	cfg.watchChannelTable.GrantReadData(webhookLambda)

	// create an integration for our API Gateway
	integration := awsapigateway.NewLambdaIntegration(webhookLambda, nil)

	// define our API Gateway
	apiGateway := awsapigateway.NewRestApi(
		stack,
		jsii.String("scriptorAPIGateway"),
		&awsapigateway.RestApiProps{
			DefaultCorsPreflightOptions: &awsapigateway.CorsOptions{
				AllowHeaders: jsii.Strings("Content-Type", "Authorization"),
				AllowMethods: jsii.Strings("POST", "PUT"),
				AllowOrigins: jsii.Strings("*"),
			},
			DeployOptions: &awsapigateway.StageOptions{
				LoggingLevel: awsapigateway.MethodLoggingLevel_INFO,
			},
			EndpointConfiguration: &awsapigateway.EndpointConfiguration{
				Types: &[]awsapigateway.EndpointType{
					awsapigateway.EndpointType_REGIONAL,
				},
			},
		},
	)

	// Register the route for processing the webhook
	webhook := apiGateway.Root().AddResource(jsii.String("webhook"), nil)
	registerRoute := webhook.AddResource(jsii.String("google-drive"), nil)
	registerRoute.AddMethod(jsii.String("POST"), integration, nil)

	cfg.documentQueue.GrantSendMessages(webhookLambda)

	// save the webhook URL for later use
	cfg.WebhookURL = fmt.Sprintf("%swebhook/google-drive", *apiGateway.Url())

	return stack
}
