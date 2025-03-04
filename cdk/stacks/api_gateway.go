package stacks

import (
	"fmt"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsapigateway"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslambda"
	"github.com/aws/jsii-runtime-go"
)

func (cfg *CdkScriptorConfig) NewApiGatewayStack(id string) awscdk.Stack {
	stack := awscdk.NewStack(cfg.App, &id, &cfg.Props.StackProps)

	// Create the lambda for the Google Drive web hook
	myFunction := awslambda.NewFunction(stack, jsii.String("scriptorGoogleDriveWebhookLambda"), &awslambda.FunctionProps{
		Runtime: awslambda.Runtime_PROVIDED_AL2023(),
		Code:    awslambda.AssetCode_FromAsset(jsii.String("../bin/gd_webhook_lambda.zip"), nil),
		Handler: jsii.String("main"),
	})

	// grant the lambda permission to read the secrets
	cfg.GoogleServiceKeySecret.GrantRead(myFunction, nil)

	// grant r/w permissions to the lambda for the new table
	cfg.documentTable.GrantReadWriteData(myFunction)

	// grant the lambda permissions to read/write the watch channel table
	cfg.watchChannelTable.GrantReadWriteData(myFunction)

	// integrate the API Gateway with our lambda
	integration := awsapigateway.NewLambdaIntegration(myFunction, nil)

	// define our api gateway
	apiGateway := awsapigateway.NewRestApi(stack, jsii.String("scriptorAPIGateway"), &awsapigateway.RestApiProps{
		DefaultCorsPreflightOptions: &awsapigateway.CorsOptions{
			AllowHeaders: jsii.Strings("Content-Type", "Authorization"),
			AllowMethods: jsii.Strings("GET", "POST", "DELETE", "PUT", "OPTIONS"),
			AllowOrigins: jsii.Strings("*"),
		},
		DeployOptions: &awsapigateway.StageOptions{
			LoggingLevel: awsapigateway.MethodLoggingLevel_INFO,
		},
		EndpointConfiguration: &awsapigateway.EndpointConfiguration{
			Types: &[]awsapigateway.EndpointType{awsapigateway.EndpointType_REGIONAL},
		},
	})

	// Register the route for processing the webhook
	webhook := apiGateway.Root().AddResource(jsii.String("webhook"), nil)
	registerRoute := webhook.AddResource(jsii.String("google-drive"), nil)
	registerRoute.AddMethod(jsii.String("POST"), integration, nil)

	// save the webhook URL
	cfg.WebhookURL = fmt.Sprintf("%swebhook/google-drive", *apiGateway.Url())

	return stack
}
