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

func (cfg *CdkScriptorConfig) NewDocumentWorkflowStack(id string) awscdk.Stack {
	stack := awscdk.NewStack(cfg.App, &id, &cfg.Props.StackProps)

	stateMachine := cfg.configureStateMachine(stack)

	// configure the download lambda and pass it the state machine ARN so it can start things off
	_ = cfg.configureDownloadLambda(stack, stateMachine)

	return stack
}

func (cfg *CdkScriptorConfig) configureDownloadLambda(stack awscdk.Stack, stateMachine awsstepfunctions.StateMachine) awslambda.Function {

	// Define Lambda functions for workflow steps
	downloadLambda := awslambda.NewFunction(stack, jsii.String("scriptorDownloadLambda"), &awslambda.FunctionProps{
		Runtime: awslambda.Runtime_PROVIDED_AL2(),
		Code:    awslambda.AssetCode_FromAsset(jsii.String("../bin/workflow_download.zip"), nil), // Path to compiled Go binary
		Handler: jsii.String("main"),
		Timeout: awscdk.Duration_Minutes(jsii.Number(5)),
		Environment: &map[string]*string{
			"STATE_MACHINE_ARN": jsii.String(*stateMachine.StateMachineArn()),
		},
	})

	// grant the lambda permission to start the state machine
	stateMachine.GrantStartExecution(downloadLambda)

	// grant lambda permissions to read the secrets
	cfg.GoogleServiceKeySecret.GrantRead(downloadLambda, nil)

	// grant the lambda r/w permissions to the document table
	cfg.documentTable.GrantReadWriteData(downloadLambda)

	// grant the lambda read permissions to the watch channel table
	cfg.watchChannelTable.GrantReadData(downloadLambda)

	// grant the lambda read/write permissions to the S3 staging bucket
	cfg.documentBucket.GrantReadWrite(downloadLambda, nil)

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

func (cfg *CdkScriptorConfig) configureStateMachine(stack awscdk.Stack) awsstepfunctions.StateMachine {
	mathpixProcessLambda := awslambda.NewFunction(stack, jsii.String("scriptorMathpixProcess"), &awslambda.FunctionProps{
		Runtime: awslambda.Runtime_PROVIDED_AL2(),
		Code:    awslambda.AssetCode_FromAsset(jsii.String("../bin/workflow_mathpix_process.zip"), nil),
		Handler: jsii.String("main"),
		Timeout: awscdk.Duration_Minutes(jsii.Number(5)),
	})

	chatgptProcessLambda := awslambda.NewFunction(stack, jsii.String("scriptorChatGPTProcess"), &awslambda.FunctionProps{
		Runtime: awslambda.Runtime_PROVIDED_AL2(),
		Code:    awslambda.AssetCode_FromAsset(jsii.String("../bin/workflow_chatgpt_process.zip"), nil),
		Handler: jsii.String("main"),
		Timeout: awscdk.Duration_Minutes(jsii.Number(5)),
	})

	uploadLambda := awslambda.NewFunction(stack, jsii.String("scriptorUploadLambda"), &awslambda.FunctionProps{
		Runtime: awslambda.Runtime_PROVIDED_AL2(),
		Code:    awslambda.AssetCode_FromAsset(jsii.String("../bin/workflow_upload.zip"), nil),
		Handler: jsii.String("main"),
		Timeout: awscdk.Duration_Minutes(jsii.Number(5)),
	})

	taskTimeout := awsstepfunctions.Timeout_Duration(awscdk.Duration_Minutes(jsii.Number(3)))

	// 🔹 Step 1: Task - Send to MathPix
	mathpixTask := awsstepfunctionstasks.NewLambdaInvoke(stack, jsii.String("MathpixTask"), &awsstepfunctionstasks.LambdaInvokeProps{
		LambdaFunction: mathpixProcessLambda,
		TaskTimeout:    taskTimeout,
		OutputPath:     jsii.String("$.Payload"),
	})

	// 🔹 Step 2: Task - Send to ChatGPT
	chatgptTask := awsstepfunctionstasks.NewLambdaInvoke(stack, jsii.String("ChatGPTTask"), &awsstepfunctionstasks.LambdaInvokeProps{
		LambdaFunction: chatgptProcessLambda,
		TaskTimeout:    taskTimeout,
		OutputPath:     jsii.String("$.Payload"),
	})

	// 🔹 Step 3: Task - Upload file
	uploadTask := awsstepfunctionstasks.NewLambdaInvoke(stack, jsii.String("UploadTask"), &awsstepfunctionstasks.LambdaInvokeProps{
		LambdaFunction: uploadLambda,
		TaskTimeout:    taskTimeout,
		OutputPath:     jsii.String("$.Payload"),
	})

	// Define workflow sequence
	workflowDefinition := mathpixTask.Next(chatgptTask).Next(uploadTask)

	// Create Step Functions state machine
	stateMachine := awsstepfunctions.NewStateMachine(stack, jsii.String("FileProcessingStateMachine"), &awsstepfunctions.StateMachineProps{
		Definition: workflowDefinition,
		Timeout:    awscdk.Duration_Minutes(jsii.Number(15)), // Workflow timeout
	})

	return stateMachine
}
