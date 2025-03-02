package stacks

import (
	"fmt"

	"github.com/KyleBrandon/scriptor/pkg/types"
	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsapigateway"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsdynamodb"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslambda"
	"github.com/aws/aws-cdk-go/awscdk/v2/awssecretsmanager"
	"github.com/aws/jsii-runtime-go"
)

func (cfg *CdkScriptorConfig) NewApiGatewayStack(id string) awscdk.Stack {
	stack := awscdk.NewStack(cfg.App, &id, &cfg.Props.StackProps)

	// Reference an existing secret in AWS Secrets Manager
	cfg.GoogleServiceKeySecret = awssecretsmanager.Secret_FromSecretNameV2(stack, jsii.String(types.GOOGLE_SERVICE_KEY_SECRET), jsii.String(types.GOOGLE_SERVICE_KEY_SECRET))

	// register the Document table
	table := awsdynamodb.NewTable(stack, jsii.String("Document"), &awsdynamodb.TableProps{
		TableName: jsii.String("document"),
		PartitionKey: &awsdynamodb.Attribute{
			Name: jsii.String("id"),
			Type: awsdynamodb.AttributeType_STRING,
		},
		SortKey: &awsdynamodb.Attribute{
			Name: jsii.String("expires_at"),
			Type: awsdynamodb.AttributeType_NUMBER,
		},
	})

	// Create the lambda for the Google Drive web hook
	myFunction := awslambda.NewFunction(stack, jsii.String("scriptorGoogleDriveWebhookLambda"), &awslambda.FunctionProps{
		Runtime: awslambda.Runtime_PROVIDED_AL2023(),
		Code:    awslambda.AssetCode_FromAsset(jsii.String("../bin/gd_webhook_lambda.zip"), nil),
		Handler: jsii.String("main"),
	})

	// grant the lambda permission to read the secrets
	cfg.GoogleServiceKeySecret.GrantRead(myFunction, nil)

	// grant r/w permissions to the lambda for the new table
	table.GrantReadWriteData(myFunction)

	// define our api gateway
	api := awsapigateway.NewRestApi(stack, jsii.String("scriptorAPIGateway"), &awsapigateway.RestApiProps{
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

	// integrate the API Gateway with our lambda
	integration := awsapigateway.NewLambdaIntegration(myFunction, nil)

	// Register the route for processing the webhook
	webhook := api.Root().AddResource(jsii.String("webhook"), nil)
	registerRoute := webhook.AddResource(jsii.String("google-drive"), nil)
	registerRoute.AddMethod(jsii.String("POST"), integration, nil)

	// save the webhook URL
	cfg.WebhookURL = fmt.Sprintf("%swebhook/google-drive", *api.Url())

	return stack
}
