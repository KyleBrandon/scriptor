package stacks

import (
	"fmt"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsapigateway"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslambda"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsstepfunctions"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsstepfunctionstasks"
	"github.com/aws/jsii-runtime-go"
)

func (cfg *CdkScriptorConfig) configureDownloadLambda(stack awscdk.Stack) awslambda.Function {

	// Define Lambda functions for workflow steps
	downloadLambda := awslambda.NewFunction(stack, jsii.String("scriptorDownloadLambda"), &awslambda.FunctionProps{
		Runtime: awslambda.Runtime_PROVIDED_AL2(),
		Code:    awslambda.AssetCode_FromAsset(jsii.String("../bin/workflow_download.zip"), nil), // Path to compiled Go binary
		Handler: jsii.String("main"),
	})

	// grant lambda permissions to read the secrets
	cfg.GoogleServiceKeySecret.GrantRead(downloadLambda, nil)

	// grant the lambda r/w permissions to the document table
	cfg.documentTable.GrantReadWriteData(downloadLambda)

	// grant the lambda read permissions to the watch channel table
	cfg.watchChannelTable.GrantReadData(downloadLambda)

	// create an integration for our API Gateway
	integration := awsapigateway.NewLambdaIntegration(downloadLambda, nil)

	// define our API Gateway
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

	// save the webhook URL for later use
	cfg.WebhookURL = fmt.Sprintf("%swebhook/google-drive", *apiGateway.Url())

	return downloadLambda

}

func (cfg *CdkScriptorConfig) NewDocumentWorkflowStack(id string) awscdk.Stack {
	stack := awscdk.NewStack(cfg.App, &id, &cfg.Props.StackProps)

	downloadLambda := cfg.configureDownloadLambda(stack)

	mathpixProcessLambda := awslambda.NewFunction(stack, jsii.String("scriptorMathpixProcess"), &awslambda.FunctionProps{
		Runtime: awslambda.Runtime_PROVIDED_AL2(),
		Code:    awslambda.AssetCode_FromAsset(jsii.String("../bin/workflow_mathpix_process.zip"), nil),
		Handler: jsii.String("main"),
	})

	chatgptProcessLambda := awslambda.NewFunction(stack, jsii.String("scriptorChatGPTProcess"), &awslambda.FunctionProps{
		Runtime: awslambda.Runtime_PROVIDED_AL2(),
		Code:    awslambda.AssetCode_FromAsset(jsii.String("../bin/workflow_chatgpt_process.zip"), nil),
		Handler: jsii.String("main"),
	})

	uploadLambda := awslambda.NewFunction(stack, jsii.String("scriptorUploadLambda"), &awslambda.FunctionProps{
		Runtime: awslambda.Runtime_PROVIDED_AL2(),
		Code:    awslambda.AssetCode_FromAsset(jsii.String("../bin/workflow_upload.zip"), nil),
		Handler: jsii.String("main"),
	})

	// 🔹 Step 1: Task - Download file
	downloadTask := awsstepfunctionstasks.NewLambdaInvoke(stack, jsii.String("DownloadTask"), &awsstepfunctionstasks.LambdaInvokeProps{
		LambdaFunction: downloadLambda,
	})

	// 🔹 Step 2: Task - Send to MathPix
	mathpixTask := awsstepfunctionstasks.NewLambdaInvoke(stack, jsii.String("MathpixTask"), &awsstepfunctionstasks.LambdaInvokeProps{
		LambdaFunction: mathpixProcessLambda,
	})

	// 🔹 Step 3: Task - Send to ChatGPT
	chatgptTask := awsstepfunctionstasks.NewLambdaInvoke(stack, jsii.String("ChatGPTTask"), &awsstepfunctionstasks.LambdaInvokeProps{
		LambdaFunction: chatgptProcessLambda,
	})

	// 🔹 Step 4: Task - Upload file
	uploadTask := awsstepfunctionstasks.NewLambdaInvoke(stack, jsii.String("UploadTask"), &awsstepfunctionstasks.LambdaInvokeProps{
		LambdaFunction: uploadLambda,
	})

	// Define workflow sequence
	workflowDefinition := downloadTask.Next(mathpixTask).Next(chatgptTask).Next(uploadTask)

	// Create Step Functions state machine
	cfg.stateMachine = awsstepfunctions.NewStateMachine(stack, jsii.String("FileProcessingStateMachine"), &awsstepfunctions.StateMachineProps{
		Definition: workflowDefinition,
		Timeout:    awscdk.Duration_Minutes(jsii.Number(10)), // Workflow timeout
	})
	return stack
}
