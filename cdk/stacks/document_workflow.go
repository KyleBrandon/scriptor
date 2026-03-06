package stacks

import (
	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslambda"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsstepfunctions"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsstepfunctionstasks"
	"github.com/aws/jsii-runtime-go"
)

func (cfg *CdkScriptorConfig) NewDocumentWorkflowStack(id string) awscdk.Stack {
	stack := awscdk.NewStack(cfg.App, &id, &cfg.Props.StackProps)

	cfg.configureStateMachine(stack)

	return stack
}

func (cfg *CdkScriptorConfig) configureDownloadLambda(
	stack awscdk.Stack,
) awslambda.Function {

	// Define Lambda functions for workflow steps
	downloadLambda := awslambda.NewFunction(
		stack,
		jsii.String("scriptorDownloadLambda"),
		&awslambda.FunctionProps{
			Runtime: awslambda.Runtime_PROVIDED_AL2023(),
			Code: awslambda.AssetCode_FromAsset(
				jsii.String("../bin/workflow_download.zip"),
				nil,
			), // Path to compiled Go binary
			Handler: jsii.String("main"),
			Timeout: awscdk.Duration_Minutes(jsii.Number(5)),
		},
	)

	// grant lambda permissions to read the secrets
	cfg.GoogleServiceKeySecret.GrantRead(downloadLambda, nil)

	// grant the lambda r/w permissions to the document table
	cfg.documentTable.GrantReadWriteData(downloadLambda)

	// grant the lambda r/w permissions to the document stage table
	cfg.documentProcessingStageTable.GrantReadWriteData(downloadLambda)

	// grant the lambda read/write permissions to the S3 staging bucket
	cfg.documentBucket.GrantReadWrite(downloadLambda, nil)

	return downloadLambda

}

func (cfg *CdkScriptorConfig) configureMathpixLambda(
	stack awscdk.Stack,
) awslambda.Function {
	mathpixLambda := awslambda.NewFunction(
		stack,
		jsii.String("scriptorMathpixProcess"),
		&awslambda.FunctionProps{
			Runtime: awslambda.Runtime_PROVIDED_AL2023(),
			Code: awslambda.AssetCode_FromAsset(
				jsii.String("../bin/workflow_mathpix_process.zip"),
				nil,
			),
			Handler: jsii.String("main"),
			Timeout: awscdk.Duration_Minutes(jsii.Number(5)),
		},
	)

	// grant lambda permissions to read the secrets
	cfg.MathpixSecrets.GrantRead(mathpixLambda, nil)

	// grant the lambda read/write permissions to the S3 staging bucket
	cfg.documentBucket.GrantReadWrite(mathpixLambda, nil)

	// grant the lambda r/w permissions to the document table
	cfg.documentTable.GrantReadWriteData(mathpixLambda)

	// grant the lambda r/w permissions to the document table
	cfg.documentProcessingStageTable.GrantReadWriteData(mathpixLambda)

	return mathpixLambda
}

func (cfg *CdkScriptorConfig) configureClaudeLambda(
	stack awscdk.Stack,
) awslambda.Function {
	claudeLambda := awslambda.NewFunction(
		stack,
		jsii.String("scriptorClaudeProcess"),
		&awslambda.FunctionProps{
			Runtime: awslambda.Runtime_PROVIDED_AL2023(),
			Code: awslambda.AssetCode_FromAsset(
				jsii.String("../bin/workflow_claude_process.zip"),
				nil,
			),
			Handler: jsii.String("main"),
			Timeout: awscdk.Duration_Minutes(jsii.Number(5)),
		},
	)

	// grant the lambda permission to read the Claude API key secret
	cfg.ClaudeSecrets.GrantRead(claudeLambda, nil)

	// grant the lambda read/write permissions to the S3 staging bucket
	cfg.documentBucket.GrantReadWrite(claudeLambda, nil)

	// grant the lambda r/w permissions to the document table
	cfg.documentProcessingStageTable.GrantReadWriteData(claudeLambda)

	return claudeLambda
}

func (cfg *CdkScriptorConfig) configureUploadLambda(
	stack awscdk.Stack,
) awslambda.Function {
	uploadLambda := awslambda.NewFunction(
		stack,
		jsii.String("scriptorUploadLambda"),
		&awslambda.FunctionProps{
			Runtime: awslambda.Runtime_PROVIDED_AL2023(),
			Code: awslambda.AssetCode_FromAsset(
				jsii.String("../bin/workflow_upload.zip"),
				nil,
			),
			Handler: jsii.String("main"),
			Timeout: awscdk.Duration_Minutes(jsii.Number(5)),
		},
	)
	// grant the lambda read/write permissions to the S3 staging bucket
	cfg.documentBucket.GrantReadWrite(uploadLambda, nil)
	// grant the lambda r/w permissions to the document table
	cfg.documentTable.GrantReadWriteData(uploadLambda)
	// grant the lambda r/w permissions to the document table
	cfg.documentProcessingStageTable.GrantReadWriteData(uploadLambda)
	// grant lambda read permissions to Google Drive API key
	cfg.GoogleServiceKeySecret.GrantRead(uploadLambda, nil)
	// grant lambda r/w permissions to the default Google Drive folders
	cfg.DefaultFoldersSecret.GrantRead(uploadLambda, nil)

	return uploadLambda
}

func (cfg *CdkScriptorConfig) configureStateMachine(stack awscdk.Stack) {
	downloadLambda := cfg.configureDownloadLambda(stack)
	mathpixLambda := cfg.configureMathpixLambda(stack)
	claudeLambda := cfg.configureClaudeLambda(stack)
	uploadLambda := cfg.configureUploadLambda(stack)

	taskTimeout := awsstepfunctions.Timeout_Duration(
		awscdk.Duration_Minutes(jsii.Number(3)),
	)

	downloadTask := awsstepfunctionstasks.NewLambdaInvoke(
		stack,
		jsii.String("DownloadTask"),
		&awsstepfunctionstasks.LambdaInvokeProps{
			LambdaFunction: downloadLambda,
			TaskTimeout:    taskTimeout,
			OutputPath:     jsii.String("$.Payload"),
		},
	)

	mathpixTask := awsstepfunctionstasks.NewLambdaInvoke(
		stack,
		jsii.String("MathpixTask"),
		&awsstepfunctionstasks.LambdaInvokeProps{
			LambdaFunction: mathpixLambda,
			TaskTimeout:    taskTimeout,
			OutputPath:     jsii.String("$.Payload"),
		},
	)

	claudeTask := awsstepfunctionstasks.NewLambdaInvoke(
		stack,
		jsii.String("ClaudeTask"),
		&awsstepfunctionstasks.LambdaInvokeProps{
			LambdaFunction: claudeLambda,
			TaskTimeout:    taskTimeout,
			OutputPath:     jsii.String("$.Payload"),
		},
	)

	uploadTask := awsstepfunctionstasks.NewLambdaInvoke(
		stack,
		jsii.String("UploadTask"),
		&awsstepfunctionstasks.LambdaInvokeProps{
			LambdaFunction: uploadLambda,
			TaskTimeout:    taskTimeout,
			OutputPath:     jsii.String("$.Payload"),
		},
	)

	// Define workflow sequence
	workflowDefinition := downloadTask.Next(mathpixTask).
		Next(claudeTask).
		Next(uploadTask)

	// Create Step Functions state machine
	cfg.stateMachine = awsstepfunctions.NewStateMachine(
		stack,
		jsii.String("FileProcessingStateMachine"),
		&awsstepfunctions.StateMachineProps{
			Definition: workflowDefinition,
			Timeout: awscdk.Duration_Minutes(
				jsii.Number(15),
			), // Workflow timeout
		},
	)
}
